package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tourplannerbot/internal/audioinput"
	"tourplannerbot/internal/buildinfo"
	"tourplannerbot/internal/config"
	"tourplannerbot/internal/database"
	"tourplannerbot/internal/llm"
	"tourplannerbot/internal/prompt"
	"tourplannerbot/internal/telegram"
	applicationTools "tourplannerbot/internal/tools"
	"tourplannerbot/internal/tools/currenttime"
	"tourplannerbot/internal/tools/draft"
	"tourplannerbot/internal/tools/mcpclient"
	"tourplannerbot/internal/webapp"

	"github.com/go-telegram/bot"
	"github.com/labstack/echo/v5"
	"gorm.io/gorm"
)

func main() {
	if err := run(); err != nil {
		slog.Error("tourplannerbot stopped", "error", err)
		os.Exit(1)
	}
}

// run initializes the HTTP server and Telegram bot, returning errors only after
// deferred shutdown handlers have drained active resources.
func run() error {
	applicationConfig, err := config.LoadFromEnvironment()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	logLevel := slog.LevelInfo
	if applicationConfig.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	buildInformation := buildinfo.Current()
	logger.Info("starting tourplannerbot",
		"log_level", applicationConfig.LogLevel,
		"version", buildInformation.Version,
		"build_time", buildInformation.BuildTime,
	)
	signalContext, cancelSignalContext := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelSignalContext()
	applicationContext, cancelApplicationContext := context.WithCancel(signalContext)
	defer cancelApplicationContext()

	databaseReadiness := database.NewReadiness()
	allowedUserAuthorizer := webapp.NewGORMAllowedUserAuthorizer()
	draftReader := webapp.NewGORMDraftReader()
	sessionAuthenticator, err := webapp.NewSessionAuthenticator(webapp.SessionConfig{
		TelegramBotToken:     applicationConfig.TelegramBotToken,
		AuthenticationMaxAge: applicationConfig.TelegramWebAppAuthMaxAge,
	}, allowedUserAuthorizer)
	if err != nil {
		return fmt.Errorf("initialize Mini App authentication: %w", err)
	}
	echoServer := webapp.NewServer(logger, databaseReadiness, buildInformation, sessionAuthenticator, draftReader)
	startHTTPServer(applicationContext, cancelApplicationContext, logger, applicationConfig.Port, echoServer)

	toolRegistry := applicationTools.NewRegistry()
	if applicationConfig.Tools.CurrentTime.Enabled {
		currentTimeTool, err := currenttime.New(applicationConfig.Tools.CurrentTime.DefaultTimezone)
		if err != nil {
			logger.Error("failed to initialize current_time tool", "error", err)
			return fmt.Errorf("initialize current_time tool: %w", err)
		}
		if err := toolRegistry.Register(currentTimeTool); err != nil {
			logger.Error("failed to register current_time tool", "error", err)
			return fmt.Errorf("register current_time tool: %w", err)
		}
	}
	mcpConnections := initializeMCPServers(applicationContext, logger, applicationConfig.Tools.MCPServers, toolRegistry)
	defer closeMCPConnections(logger, mcpConnections)

	databaseConnection, err := openDatabaseWithRetry(applicationContext, logger, applicationConfig.DatabaseURL)
	if err != nil {
		logger.Info("application stopped before PostgreSQL became available", "error", err)
		return fmt.Errorf("wait for PostgreSQL: %w", err)
	}

	sqlDatabaseConnection, err := databaseConnection.DB()
	if err != nil {
		logger.Error("failed to access database connection pool", "error", err)
		return fmt.Errorf("access database connection pool: %w", err)
	}
	databaseReadiness.SetConnection(sqlDatabaseConnection)
	allowedUserAuthorizer.SetDatabaseConnection(databaseConnection)
	draftReader.SetDatabaseConnection(databaseConnection)
	defer sqlDatabaseConnection.Close()
	createDraftTool, err := draft.New(databaseConnection)
	if err != nil {
		logger.Error("failed to initialize create_draft tool", "error", err)
		return fmt.Errorf("initialize create_draft tool: %w", err)
	}
	if err := toolRegistry.Register(createDraftTool); err != nil {
		logger.Error("failed to register create_draft tool", "error", err)
		return fmt.Errorf("register create_draft tool: %w", err)
	}
	updateDraftTool, err := draft.NewUpdate(databaseConnection)
	if err != nil {
		logger.Error("failed to initialize update_draft tool", "error", err)
		return fmt.Errorf("initialize update_draft tool: %w", err)
	}
	if err := toolRegistry.Register(updateDraftTool); err != nil {
		logger.Error("failed to register update_draft tool", "error", err)
		return fmt.Errorf("register update_draft tool: %w", err)
	}

	registeredToolDefinitions := toolRegistry.Definitions()
	registeredToolNames := make([]string, 0, len(registeredToolDefinitions))
	for _, registeredToolDefinition := range registeredToolDefinitions {
		registeredToolNames = append(registeredToolNames, registeredToolDefinition.Name)
	}
	logger.Info("tools initialized", "enabled_tools", registeredToolNames)

	llmClient := llm.NewClient(
		applicationConfig.OpenAIAPIKey,
		applicationConfig.OpenAIBaseURL,
		applicationConfig.LLMProvider,
		applicationConfig.OpenAIModel,
		applicationConfig.LLMMaxTokens,
		applicationConfig.LLMPricing,
	)
	voiceInputProcessor := audioinput.NewProcessor(
		applicationConfig.OpenAIAPIKey,
		applicationConfig.OpenAIBaseURL,
		applicationConfig.OpenAITranscriptionModel,
		applicationConfig.OpenAIModel,
	)
	systemInstructions, err := prompt.Load("prompts/system_query.md")
	if err != nil {
		logger.Error("failed to load query system prompt", "error", err)
		return fmt.Errorf("load query system prompt: %w", err)
	}
	messageHandler := telegram.NewHandler(
		logger,
		databaseConnection,
		applicationConfig.AccessPIN,
		applicationConfig.AppBaseURL,
		systemInstructions,
		applicationConfig.LLMHistoryMaxMessages,
		applicationConfig.ToolCallMaxIterations,
		toolRegistry,
		llmClient,
		voiceInputProcessor,
	)

	telegramBot, err := bot.New(applicationConfig.TelegramBotToken,
		bot.WithDefaultHandler(messageHandler.HandleMessage),
	)
	if err != nil {
		logger.Error("failed to create telegram bot", "error", err)
		return fmt.Errorf("create Telegram bot: %w", err)
	}

	botInfo, err := telegramBot.GetMe(applicationContext)
	if err != nil {
		logger.Error("failed to get bot info from telegram", "error", err)
		return fmt.Errorf("get Telegram bot info: %w", err)
	}
	messageHandler.SetBotUsername(botInfo.Username)
	logger.Info("connected to telegram",
		"bot_id", botInfo.ID,
		"bot_username", botInfo.Username,
		"bot_name", botInfo.FirstName,
		"can_join_groups", botInfo.CanJoinGroups,
		"can_read_all_group_messages", botInfo.CanReadAllGroupMessages,
	)

	logger.Info("bot is running, press Ctrl+C to stop")
	telegramBot.Start(applicationContext)
	logger.Info("bot stopped gracefully")
	return nil
}

// startHTTPServer starts Echo independently from the Telegram bot. Echo uses
// the shared context to drain requests during shutdown and a fatal listener
// error cancels the rest of the application.
func startHTTPServer(applicationContext context.Context, cancelApplicationContext context.CancelFunc, logger *slog.Logger, port int, echoServer *echo.Echo) {
	go func() {
		echoStartConfiguration := echo.StartConfig{
			Address:         fmt.Sprintf("0.0.0.0:%d", port),
			GracefulTimeout: 10 * time.Second,
			BeforeServeFunc: func(httpServer *http.Server) error {
				httpServer.ReadHeaderTimeout = 5 * time.Second
				return nil
			},
		}
		if err := echoStartConfiguration.Start(applicationContext, echoServer); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Echo HTTP server stopped unexpectedly", "error", err)
			cancelApplicationContext()
		}
	}()
}

// openDatabaseWithRetry waits for PostgreSQL while the liveness endpoint stays
// available and readiness reports a temporary failure.
func openDatabaseWithRetry(applicationContext context.Context, logger *slog.Logger, databaseURL string) (*gorm.DB, error) {
	const databaseRetryInterval = 5 * time.Second

	for {
		databaseConnection, err := database.Open(applicationContext, databaseURL)
		if err == nil {
			return databaseConnection, nil
		}
		logger.Warn("PostgreSQL unavailable; retrying", "retry_in", databaseRetryInterval.String(), "error", err)

		retryTimer := time.NewTimer(databaseRetryInterval)
		select {
		case <-applicationContext.Done():
			if !retryTimer.Stop() {
				<-retryTimer.C
			}
			return nil, applicationContext.Err()
		case <-retryTimer.C:
		}
	}
}

// initializeMCPServers connects enabled Streamable HTTP servers independently.
// A failed server is logged and skipped so other tools remain available.
func initializeMCPServers(applicationContext context.Context, logger *slog.Logger, serverConfigurations []config.MCPServerConfig, toolRegistry *applicationTools.Registry) []*mcpclient.Connection {
	mcpConnections := make([]*mcpclient.Connection, 0, len(serverConfigurations))
	for _, serverConfiguration := range serverConfigurations {
		if !serverConfiguration.Enabled {
			logger.Debug(fmt.Sprintf("MCP %s is disabled", serverConfiguration.Name),
				"mcp_name", serverConfiguration.Name,
				"status", "disabled",
			)
			continue
		}

		initializationStartedAt := time.Now()
		logger.Info(fmt.Sprintf("loading MCP %s", serverConfiguration.Name),
			"mcp_name", serverConfiguration.Name,
			"mcp_url", serverConfiguration.URL,
			"status", "loading",
		)
		initializationContext, cancelInitialization := context.WithTimeout(applicationContext, serverConfiguration.CallTimeout)
		mcpConnection, err := mcpclient.Connect(initializationContext, mcpclient.Config{
			Name:           serverConfiguration.Name,
			URL:            serverConfiguration.URL,
			AuthType:       serverConfiguration.AuthType,
			Token:          serverConfiguration.Token,
			RequestTimeout: serverConfiguration.CallTimeout,
		})
		cancelInitialization()
		initializationDuration := time.Since(initializationStartedAt)
		if err != nil {
			logger.Warn(fmt.Sprintf("MCP %s disabled after initialization failure in %s", serverConfiguration.Name, initializationDuration),
				"mcp_name", serverConfiguration.Name,
				"mcp_url", serverConfiguration.URL,
				"duration_ms", initializationDuration.Milliseconds(),
				"status", "disabled",
				"error", err,
			)
			continue
		}

		discoveredTools := mcpConnection.Tools()
		if err := toolRegistry.RegisterAll(discoveredTools); err != nil {
			_ = mcpConnection.Close()
			logger.Warn(fmt.Sprintf("MCP %s disabled after tool registration failure in %s", serverConfiguration.Name, initializationDuration),
				"mcp_name", serverConfiguration.Name,
				"mcp_url", serverConfiguration.URL,
				"duration_ms", initializationDuration.Milliseconds(),
				"status", "disabled",
				"error", err,
			)
			continue
		}

		mcpConnections = append(mcpConnections, mcpConnection)
		logger.Info(fmt.Sprintf("MCP %s loaded %d tools in %s", serverConfiguration.Name, len(discoveredTools), initializationDuration),
			"mcp_name", serverConfiguration.Name,
			"mcp_url", serverConfiguration.URL,
			"tool_count", len(discoveredTools),
			"duration_ms", initializationDuration.Milliseconds(),
			"status", "ready",
		)
	}
	return mcpConnections
}

// closeMCPConnections closes all successfully initialized MCP sessions during
// application shutdown and reports any close failures.
func closeMCPConnections(logger *slog.Logger, mcpConnections []*mcpclient.Connection) {
	for _, mcpConnection := range mcpConnections {
		if err := mcpConnection.Close(); err != nil {
			logger.Warn("failed to close MCP connection", "error", err)
		}
	}
}
