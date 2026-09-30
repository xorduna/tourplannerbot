package monei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"tourplannerbot/internal/tools"
)

const (
	createPaymentLinkToolName = "create_monei_payment_link"
	getPaymentToolName        = "get_monei_payment"
	maximumSummaryLength      = 2000
	maximumCustomerNameLength = 255
)

var (
	paymentIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	decimalEuroAmountRegex  = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,2})?$`)
	supportedPaymentMethods = map[string]bool{"bizum": true, "card": true}
)

// CreatePaymentLinkTool creates a MONEI payment and returns Diana's canonical
// public link along with the full payment object.
type CreatePaymentLinkTool struct {
	client *Client
	now    func() time.Time
}

// GetPaymentTool retrieves the current MONEI status for one payment ID.
type GetPaymentTool struct {
	client *Client
}

// NewCreatePaymentLink creates the native MONEI payment-link creation tool.
func NewCreatePaymentLink(client *Client) (*CreatePaymentLinkTool, error) {
	if client == nil {
		return nil, fmt.Errorf("MONEI client is required")
	}
	return &CreatePaymentLinkTool{client: client, now: time.Now}, nil
}

// NewGetPayment creates the native MONEI payment lookup tool.
func NewGetPayment(client *Client) (*GetPaymentTool, error) {
	if client == nil {
		return nil, fmt.Errorf("MONEI client is required")
	}
	return &GetPaymentTool{client: client}, nil
}

// Definition describes create_monei_payment_link to the LLM.
func (createPaymentLinkTool *CreatePaymentLinkTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        createPaymentLinkToolName,
		Description: "Create a one-off EUR MONEI payment link. This tool is deliberately deal-agnostic: pass the exact agreed amount, customer details, expiration date, order ID, and a concise tour summary. The amount is an EUR decimal string such as \"125.00\"; the tool converts it safely to cents. It returns the payment ID and the canonical Diana Barcelona payment link.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"amount": map[string]any{
					"type":        "string",
					"description": "Positive EUR amount with up to two decimal places, for example 125.00. Do not convert it to cents yourself.",
				},
				"customer_email": map[string]any{
					"type":        "string",
					"description": "Customer email address.",
				},
				"customer_name": map[string]any{
					"type":        "string",
					"description": "Customer full name.",
				},
				"expiration_date": map[string]any{
					"type":        "string",
					"description": "Future ISO date (YYYY-MM-DD, expiring at 23:59:59 Europe/Madrid) or future RFC 3339 timestamp.",
				},
				"order_id": map[string]any{
					"type":        "string",
					"description": "Your system's order ID.",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Tour summary stored as MONEI metadata.",
				},
				"allowed_payment_methods": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string", "enum": []string{"bizum", "card"}},
					"description": "Allowed methods. Pass [\"bizum\", \"card\"] unless Diana asks for a subset.",
				},
			},
			"required":             []string{"amount", "customer_email", "customer_name", "expiration_date", "order_id", "summary", "allowed_payment_methods"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "monei",
	}
}

// Definition describes get_monei_payment to the LLM.
func (getPaymentTool *GetPaymentTool) Definition() tools.Definition {
	return tools.Definition{
		Name:        getPaymentToolName,
		Description: "Retrieve the complete current MONEI payment object, including its status, by the opaque payment ID. When the ID is stored in a Bigin payment link, first extract the final path segment from that link.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"payment_id": map[string]any{
					"type":        "string",
					"description": "The MONEI payment ID, not a full URL.",
				},
			},
			"required":             []string{"payment_id"},
			"additionalProperties": false,
		},
		Strict: true,
		Source: "monei",
	}
}

// Execute validates the neutral business inputs, creates a MONEI payment, and
// appends the configured Diana Barcelona public payment link to the response.
func (createPaymentLinkTool *CreatePaymentLinkTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		Amount                string   `json:"amount"`
		CustomerEmail         string   `json:"customer_email"`
		CustomerName          string   `json:"customer_name"`
		ExpirationDate        string   `json:"expiration_date"`
		OrderID               string   `json:"order_id"`
		Summary               string   `json:"summary"`
		AllowedPaymentMethods []string `json:"allowed_payment_methods"`
	}{}
	if err := decodeArguments(rawArguments, &arguments, createPaymentLinkToolName); err != nil {
		return "", err
	}

	amountInCents, err := parseEURAmount(arguments.Amount)
	if err != nil {
		return "", err
	}
	customerEmail := strings.TrimSpace(arguments.CustomerEmail)
	if !strings.Contains(customerEmail, "@") || len(customerEmail) > 254 {
		return "", fmt.Errorf("customer_email must be a valid email address")
	}
	customerName := strings.TrimSpace(arguments.CustomerName)
	if customerName == "" || !utf8.ValidString(customerName) || utf8.RuneCountInString(customerName) > maximumCustomerNameLength {
		return "", fmt.Errorf("customer_name must be valid UTF-8 and contain 1 to %d characters", maximumCustomerNameLength)
	}
	expireAt, err := parseExpirationDate(arguments.ExpirationDate, createPaymentLinkTool.now())
	if err != nil {
		return "", err
	}
	orderID := strings.TrimSpace(arguments.OrderID)
	if orderID == "" || len(orderID) > 40 || !utf8.ValidString(orderID) {
		return "", fmt.Errorf("order_id must be valid UTF-8 and contain 1 to 40 characters")
	}
	summary := strings.TrimSpace(arguments.Summary)
	if summary == "" || !utf8.ValidString(summary) || utf8.RuneCountInString(summary) > maximumSummaryLength {
		return "", fmt.Errorf("summary must be valid UTF-8 and contain 1 to %d characters", maximumSummaryLength)
	}
	allowedPaymentMethods, err := validatePaymentMethods(arguments.AllowedPaymentMethods)
	if err != nil {
		return "", err
	}

	requestBody := struct {
		Amount                int      `json:"amount"`
		Currency              string   `json:"currency"`
		OrderID               string   `json:"orderId"`
		Customer              any      `json:"customer"`
		ExpireAt              int64    `json:"expireAt"`
		AllowedPaymentMethods []string `json:"allowedPaymentMethods"`
		Metadata              any      `json:"metadata"`
	}{
		Amount:                amountInCents,
		Currency:              "EUR",
		OrderID:               orderID,
		Customer:              map[string]string{"email": customerEmail, "name": customerName},
		ExpireAt:              expireAt.Unix(),
		AllowedPaymentMethods: allowedPaymentMethods,
		Metadata:              map[string]string{"summary": summary},
	}
	responseBody, err := createPaymentLinkTool.client.CreatePayment(ctx, requestBody)
	if err != nil {
		return "", fmt.Errorf("create MONEI payment: %w", err)
	}
	return addCanonicalPaymentLink(responseBody, createPaymentLinkTool.client)
}

// Execute validates a payment ID and returns MONEI's complete current payment object.
func (getPaymentTool *GetPaymentTool) Execute(ctx context.Context, rawArguments json.RawMessage) (string, error) {
	arguments := struct {
		PaymentID string `json:"payment_id"`
	}{}
	if err := decodeArguments(rawArguments, &arguments, getPaymentToolName); err != nil {
		return "", err
	}
	paymentID := strings.TrimSpace(arguments.PaymentID)
	if !paymentIDPattern.MatchString(paymentID) {
		return "", fmt.Errorf("payment_id must be an opaque MONEI payment ID")
	}
	responseBody, err := getPaymentTool.client.GetPayment(ctx, paymentID)
	if err != nil {
		return "", fmt.Errorf("retrieve MONEI payment: %w", err)
	}
	return string(responseBody), nil
}

// decodeArguments rejects unknown fields and multiple JSON values for a tool call.
func decodeArguments(rawArguments json.RawMessage, target any, toolName string) error {
	decoder := json.NewDecoder(bytes.NewReader(rawArguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s arguments: %w", toolName, err)
	}
	var trailingValue any
	if err := decoder.Decode(&trailingValue); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode %s arguments: multiple JSON values are not allowed", toolName)
		}
		return fmt.Errorf("decode %s arguments: %w", toolName, err)
	}
	return nil
}

// parseEURAmount converts an exact EUR decimal amount into MONEI's cents integer.
func parseEURAmount(rawAmount string) (int, error) {
	amount := strings.TrimSpace(rawAmount)
	if !decimalEuroAmountRegex.MatchString(amount) {
		return 0, fmt.Errorf("amount must be a positive EUR decimal with up to two decimal places")
	}
	parts := strings.SplitN(amount, ".", 2)
	euros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount is too large")
	}
	cents := int64(0)
	if len(parts) == 2 {
		centsText := parts[1] + "00"
		cents, err = strconv.ParseInt(centsText[:2], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("amount is invalid")
		}
	}
	if euros > (math.MaxInt32-cents)/100 {
		return 0, fmt.Errorf("amount is too large")
	}
	amountInCents := euros*100 + cents
	if amountInCents <= 0 {
		return 0, fmt.Errorf("amount must be greater than zero")
	}
	return int(amountInCents), nil
}

// parseExpirationDate accepts a date in Barcelona time or an exact RFC 3339 timestamp.
func parseExpirationDate(rawExpirationDate string, now time.Time) (time.Time, error) {
	expirationDate := strings.TrimSpace(rawExpirationDate)
	var parsedExpirationDate time.Time
	var err error
	if len(expirationDate) == len("2006-01-02") {
		barcelonaLocation, locationError := time.LoadLocation("Europe/Madrid")
		if locationError != nil {
			return time.Time{}, fmt.Errorf("load Europe/Madrid timezone: %w", locationError)
		}
		parsedDate, parseError := time.ParseInLocation("2006-01-02", expirationDate, barcelonaLocation)
		if parseError != nil {
			return time.Time{}, fmt.Errorf("expiration_date must be an ISO date or RFC 3339 timestamp")
		}
		parsedExpirationDate = time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 23, 59, 59, 0, barcelonaLocation)
	} else {
		parsedExpirationDate, err = time.Parse(time.RFC3339, expirationDate)
		if err != nil {
			return time.Time{}, fmt.Errorf("expiration_date must be an ISO date or RFC 3339 timestamp")
		}
	}
	if !parsedExpirationDate.After(now) {
		return time.Time{}, fmt.Errorf("expiration_date must be in the future")
	}
	return parsedExpirationDate, nil
}

// validatePaymentMethods validates and deterministically orders the requested MONEI methods.
func validatePaymentMethods(rawPaymentMethods []string) ([]string, error) {
	if len(rawPaymentMethods) == 0 {
		return []string{"bizum", "card"}, nil
	}
	uniqueMethods := make(map[string]bool, len(rawPaymentMethods))
	for _, rawPaymentMethod := range rawPaymentMethods {
		paymentMethod := strings.TrimSpace(rawPaymentMethod)
		if !supportedPaymentMethods[paymentMethod] {
			return nil, fmt.Errorf("allowed_payment_methods can contain only bizum and card")
		}
		uniqueMethods[paymentMethod] = true
	}
	paymentMethods := make([]string, 0, len(uniqueMethods))
	for paymentMethod := range uniqueMethods {
		paymentMethods = append(paymentMethods, paymentMethod)
	}
	sort.Strings(paymentMethods)
	return paymentMethods, nil
}

// addCanonicalPaymentLink adds the canonical Diana URL after checking MONEI returned an ID.
func addCanonicalPaymentLink(responseBody []byte, client *Client) (string, error) {
	var paymentResponse map[string]any
	if err := json.Unmarshal(responseBody, &paymentResponse); err != nil {
		return "", fmt.Errorf("decode MONEI payment: %w", err)
	}
	paymentID, found := paymentResponse["id"].(string)
	paymentID = strings.TrimSpace(paymentID)
	if !found || !paymentIDPattern.MatchString(paymentID) {
		return "", fmt.Errorf("MONEI payment response did not include a valid payment ID")
	}
	paymentResponse["payment_link"] = client.PaymentLink(paymentID)
	encodedResponse, err := json.Marshal(paymentResponse)
	if err != nil {
		return "", fmt.Errorf("encode MONEI payment response: %w", err)
	}
	return string(encodedResponse), nil
}
