package bigin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestUpdateDealMergesMetadataAndChangesSelectedFields verifies the tool
// updates only supplied fields and preserves the rest of metadata as JSON.
func TestUpdateDealMergesMetadataAndChangesSelectedFields(t *testing.T) {
	getRequestCount := 0
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		if request.URL.Path != "/bigin/v2/Pipelines/2034020000000489080" {
			t.Errorf("path = %q", request.URL.Path)
		}
		switch request.Method {
		case http.MethodGet:
			getRequestCount++
			if getRequestCount == 1 {
				return jsonHTTPResponse(http.StatusOK, `{"data":[{"metadata":"{\"existing\":\"value\",\"tour\":{\"language\":\"ca\"}}"}]}`), nil
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"Amount":275.5,"Payment_Link":"https://www.dianabarcelona.com/pay/af6029f80f5fc73a8ad2753eea0b1be0","metadata":"{\"existing\":\"value\",\"monei_payment_id\":\"af6029f80f5fc73a8ad2753eea0b1be0\",\"tour\":{\"language\":\"es\"}}"}]}`), nil
		case http.MethodPut:
			requestBody, _ := io.ReadAll(request.Body)
			expectedBody := `{"data":[{"Amount":275.5,"Payment_Link":"https://www.dianabarcelona.com/pay/af6029f80f5fc73a8ad2753eea0b1be0","metadata":"{\n  \"existing\": \"value\",\n  \"monei_payment_id\": \"af6029f80f5fc73a8ad2753eea0b1be0\",\n  \"tour\": {\n    \"language\": \"es\"\n  }\n}"}]}`
			if string(requestBody) != expectedBody {
				t.Errorf("body = %s, want %s", requestBody, expectedBody)
			}
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"code":"SUCCESS"}]}`), nil
		default:
			t.Errorf("method = %s", request.Method)
			return jsonHTTPResponse(http.StatusMethodNotAllowed, `{}`), nil
		}
	})
	updateDealTool, err := NewUpdateDeal(newTestClient(t, transport), UpdateDealConfig{MetadataFieldAPIName: "Metadata"})
	if err != nil {
		t.Fatalf("NewUpdateDeal returned an error: %v", err)
	}
	result, err := updateDealTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"2034020000000489080","updates":[{"path":"Amount","value":275.5},{"path":"Payment_Link","value":"https://www.dianabarcelona.com/pay/af6029f80f5fc73a8ad2753eea0b1be0"},{"path":"metadata.monei_payment_id","value":"af6029f80f5fc73a8ad2753eea0b1be0"},{"path":"metadata.tour.language","value":"es"}]}`))
	if err != nil || !strings.Contains(result, `"code":"SUCCESS"`) {
		t.Fatalf("Execute result = %q, error = %v", result, err)
	}
	if !strings.Contains(result, `"verified_updates":["Amount","Payment_Link","metadata.monei_payment_id","metadata.tour.language"]`) {
		t.Errorf("result = %s", result)
	}
	if getRequestCount != 2 {
		t.Errorf("GET request count = %d, want 2", getRequestCount)
	}
}

// TestUpdateDealFailsWhenBiginIgnoresAField verifies a successful PUT cannot
// conceal an update that is missing from Bigin's subsequent record response.
func TestUpdateDealFailsWhenBiginIgnoresAField(t *testing.T) {
	getRequestCount := 0
	transport := roundTripFunction(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/v2/token" {
			return jsonHTTPResponse(http.StatusOK, `{"access_token":"access-token","expires_in":3600}`), nil
		}
		switch request.Method {
		case http.MethodPut:
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"code":"SUCCESS"}]}`), nil
		case http.MethodGet:
			getRequestCount++
			return jsonHTTPResponse(http.StatusOK, `{"data":[{"Payment_Link":"https://www.dianabarcelona.com/pay/payment-id"}]}`), nil
		default:
			return jsonHTTPResponse(http.StatusMethodNotAllowed, `{}`), nil
		}
	})
	updateDealTool, _ := NewUpdateDeal(newTestClient(t, transport), UpdateDealConfig{MetadataFieldAPIName: "Metadata"})
	_, err := updateDealTool.Execute(context.Background(), json.RawMessage(`{"deal_id":"123","updates":[{"path":"Amount","value":275.5},{"path":"Payment_Link","value":"https://www.dianabarcelona.com/pay/payment-id"}]}`))
	if err == nil || !strings.Contains(err.Error(), "Amount") {
		t.Fatalf("Execute error = %v, want failed Amount verification", err)
	}
	if getRequestCount != 1 {
		t.Errorf("GET request count = %d, want 1", getRequestCount)
	}
}

// TestUpdateDealRejectsInvalidPathsWithoutCallingBigin verifies invalid
// metadata and direct-metadata writes are rejected before any HTTP request.
func TestUpdateDealRejectsInvalidPathsWithoutCallingBigin(t *testing.T) {
	requestCount := 0
	updateDealTool, err := NewUpdateDeal(newTestClient(t, roundTripFunction(func(_ *http.Request) (*http.Response, error) {
		requestCount++
		return jsonHTTPResponse(http.StatusInternalServerError, `{}`), nil
	})), UpdateDealConfig{MetadataFieldAPIName: "Metadata"})
	if err != nil {
		t.Fatalf("NewUpdateDeal returned an error: %v", err)
	}
	for _, rawArguments := range []string{
		`{"deal_id":"123","updates":[]}`,
		`{"deal_id":"123","updates":[{"path":"metadata","value":"{}"}]}`,
		`{"deal_id":"123","updates":[{"path":"Metadata","value":"{}"}]}`,
		`{"deal_id":"123","updates":[{"path":"metadata.bad-key","value":"x"}]}`,
	} {
		if _, err := updateDealTool.Execute(context.Background(), json.RawMessage(rawArguments)); err == nil {
			t.Errorf("Execute(%s) returned nil error", rawArguments)
		}
	}
	if requestCount != 0 {
		t.Errorf("request count = %d, want 0", requestCount)
	}
}
