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
	"tourplannerbot/internal/tools/bigin"
	"tourplannerbot/internal/tools/currenttime"
	"tourplannerbot/internal/tools/draft"
	"tourplannerbot/internal/tools/gmail"
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
		"environment", applicationConfig.Environment,
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
	dealTopicStore := database.NewTelegramDealTopicStore()
	dealTopicService, err := webapp.NewDealTopicService(applicationConfig.TelegramGroupChatID, dealTopicStore)
	if err != nil {
		return fmt.Errorf("initialize deal topic service: %w", err)
	}
	dealTopicService.SetLogger(logger)
	sessionAuthenticator, err := webapp.NewSessionAuthenticator(webapp.SessionConfig{
		TelegramBotToken:     applicationConfig.TelegramBotToken,
		AuthenticationMaxAge: applicationConfig.TelegramWebAppAuthMaxAge,
	}, allowedUserAuthorizer)
	if err != nil {
		return fmt.Errorf("initialize Mini App authentication: %w", err)
	}
	echoServer := webapp.NewServer(logger, databaseReadiness, buildInformation, sessionAuthenticator, draftReader)
	webapp.RegisterDealTopicRoutes(echoServer, dealTopicService)
	startHTTPServer(applicationContext, cancelApplicationContext, logger, applicationConfig.Port, echoServer)

	toolRegistry := applicationTools.NewRegistry()
	logger.Info("tool initialization started",
		"current_time_enabled", applicationConfig.Tools.CurrentTime.Enabled,
		"bigin_enabled", applicationConfig.Tools.Bigin.Enabled,
		"gmail_enabled", applicationConfig.Tools.Gmail.Enabled,
		"mcp_server_configuration_count", len(applicationConfig.Tools.MCPServers),
		"status", "initializing",
	)
	if applicationConfig.Tools.CurrentTime.Enabled {
		if err := initializeAndRegisterTool(logger, toolRegistry, "current_time", "native", func() (applicationTools.Tool, error) {
			return currenttime.New(applicationConfig.Tools.CurrentTime.DefaultTimezone)
		}); err != nil {
			return fmt.Errorf("initialize or register current_time tool: %w", err)
		}
	} else {
		logger.Info("tool current_time is disabled",
			"tool_name", "current_time",
			"tool_source", "native",
			"status", "disabled",
			"reason", "disabled by configuration",
		)
	}
	var biginClient *bigin.Client
	if applicationConfig.Tools.Bigin.Enabled {
		biginClientInitializationStartedAt := time.Now()
		logger.Info("initializing Bigin tool client",
			"tool_source", "bigin",
			"planned_tools", []string{"add_bigin_deal_note", "get_bigin_deal", "search_bigin_contacts"},
			"api_url", applicationConfig.Tools.Bigin.APIURL,
			"accounts_url", applicationConfig.Tools.Bigin.AccountsURL,
			"timeout", applicationConfig.Tools.Bigin.CallTimeout.String(),
			"status", "initializing",
		)
		biginClient, err = bigin.NewClient(bigin.Config{
			RefreshToken: applicationConfig.Tools.Bigin.RefreshToken,
			ClientID:     applicationConfig.Tools.Bigin.ClientID,
			ClientSecret: applicationConfig.Tools.Bigin.ClientSecret,
			AccountsURL:  applicationConfig.Tools.Bigin.AccountsURL,
			APIURL:       applicationConfig.Tools.Bigin.APIURL,
			CallTimeout:  applicationConfig.Tools.Bigin.CallTimeout,
		})
		if err != nil {
			logger.Error("failed to initialize Bigin tool client",
				"tool_source", "bigin",
				"duration_ms", time.Since(biginClientInitializationStartedAt).Milliseconds(),
				"status", "failed",
				"error", err,
			)
			return fmt.Errorf("initialize Bigin client: %w", err)
		}
		logger.Info("Bigin tool client initialized",
			"tool_source", "bigin",
			"duration_ms", time.Since(biginClientInitializationStartedAt).Milliseconds(),
			"status", "ready",
		)
		dealTopicService.SetBiginDealReader(biginClient)
		if err := initializeAndRegisterTool(logger, toolRegistry, "add_bigin_deal_note", "bigin", func() (applicationTools.Tool, error) {
			return bigin.NewAddDealNote(biginClient)
		}); err != nil {
			return fmt.Errorf("initialize or register add_bigin_deal_note tool: %w", err)
		}
		if err := initializeAndRegisterTool(logger, toolRegistry, "get_bigin_deal", "bigin", func() (applicationTools.Tool, error) {
			return bigin.NewGetDeal(biginClient)
		}); err != nil {
			return fmt.Errorf("initialize or register get_bigin_deal tool: %w", err)
		}
		if err := initializeAndRegisterTool(logger, toolRegistry, "search_bigin_contacts", "bigin", func() (applicationTools.Tool, error) {
			return bigin.NewSearchContacts(biginClient)
		}); err != nil {
			return fmt.Errorf("initialize or register search_bigin_contacts tool: %w", err)
		}
	} else {
		logger.Info("Bigin tools are disabled",
			"tool_source", "bigin",
			"planned_tools", []string{"add_bigin_deal_note", "get_bigin_deal", "search_bigin_contacts"},
			"status", "disabled",
			"reason", "OAuth credentials are not configured",
		)
	}
	if applicationConfig.Tools.Gmail.Enabled {
		gmailClientInitializationStartedAt := time.Now()
		logger.Info("initializing Gmail tool client",
			"tool_source", "gmail",
			"planned_tools", []string{"create_gmail_draft", "update_gmail_draft"},
			"api_url", applicationConfig.Tools.Gmail.APIURL,
			"timeout", applicationConfig.Tools.Gmail.CallTimeout.String(),
			"status", "initializing",
		)
		gmailClient, err := gmail.NewClient(gmail.Config{
			RefreshToken: applicationConfig.Tools.Gmail.RefreshToken,
			ClientID:     applicationConfig.Tools.Gmail.ClientID,
			ClientSecret: applicationConfig.Tools.Gmail.ClientSecret,
			OAuthURL:     applicationConfig.Tools.Gmail.OAuthURL,
			APIURL:       applicationConfig.Tools.Gmail.APIURL,
			CallTimeout:  applicationConfig.Tools.Gmail.CallTimeout,
		})
		if err != nil {
			logger.Error("failed to initialize Gmail tool client",
				"tool_source", "gmail",
				"duration_ms", time.Since(gmailClientInitializationStartedAt).Milliseconds(),
				"status", "failed",
				"error", err,
			)
			return fmt.Errorf("initialize Gmail client: %w", err)
		}
		logger.Info("Gmail tool client initialized",
			"tool_source", "gmail",
			"duration_ms", time.Since(gmailClientInitializationStartedAt).Milliseconds(),
			"status", "ready",
		)
		if err := initializeAndRegisterTool(logger, toolRegistry, "create_gmail_draft", "gmail", func() (applicationTools.Tool, error) {
			return gmail.NewCreateDraft(gmailClient)
		}); err != nil {
			return fmt.Errorf("initialize or register create_gmail_draft tool: %w", err)
		}
		if err := initializeAndRegisterTool(logger, toolRegistry, "update_gmail_draft", "gmail", func() (applicationTools.Tool, error) {
			return gmail.NewUpdateDraft(gmailClient)
		}); err != nil {
			return fmt.Errorf("initialize or register update_gmail_draft tool: %w", err)
		}
	} else {
		logger.Info("Gmail tools are disabled",
			"tool_source", "gmail",
			"planned_tools", []string{"create_gmail_draft", "update_gmail_draft"},
			"status", "disabled",
			"reason", "OAuth credentials are not configured",
		)
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
	dealTopicStore.SetDatabaseConnection(databaseConnection)
	defer sqlDatabaseConnection.Close()
	if err := initializeAndRegisterTool(logger, toolRegistry, "create_draft", "native", func() (applicationTools.Tool, error) {
		return draft.New(databaseConnection)
	}); err != nil {
		return fmt.Errorf("initialize or register create_draft tool: %w", err)
	}
	if err := initializeAndRegisterTool(logger, toolRegistry, "update_draft", "native", func() (applicationTools.Tool, error) {
		return draft.NewUpdate(databaseConnection)
	}); err != nil {
		return fmt.Errorf("initialize or register update_draft tool: %w", err)
	}

	registeredToolDefinitions := toolRegistry.Definitions()
	registeredToolNames := make([]string, 0, len(registeredToolDefinitions))
	registeredToolsBySource := make(map[string][]string)
	for _, registeredToolDefinition := range registeredToolDefinitions {
		registeredToolNames = append(registeredToolNames, registeredToolDefinition.Name)
		registeredToolsBySource[registeredToolDefinition.Source] = append(registeredToolsBySource[registeredToolDefinition.Source], registeredToolDefinition.Name)
	}
	logger.Info("tool initialization completed",
		"available_tool_count", len(registeredToolNames),
		"available_tools", registeredToolNames,
		"available_tools_by_source", registeredToolsBySource,
		"mcp_connection_count", len(mcpConnections),
		"status", "ready",
	)

	llmClient := llm.NewClient(
		applicationConfig.OpenAIAPIKey,
		applicationConfig.OpenAIBaseURL,
		applicationConfig.LLMProvider,
		applicationConfig.OpenAIModel,
		applicationConfig.LLMMaxTokens,
		applicationConfig.LLMPricing,
	)
	dealTopicIntroductionGenerator, err := webapp.NewLLMDealTopicIntroductionGenerator(llmClient)
	if err != nil {
		return fmt.Errorf("initialize deal topic introduction generator: %w", err)
	}
	dealTopicService.SetIntroductionGenerator(dealTopicIntroductionGenerator)
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
	messageHandler.SetTrustedTelegramGroupChatID(applicationConfig.TelegramGroupChatID)
	if biginClient != nil {
		messageHandler.SetBiginDealReader(biginClient)
	}

	telegramBot, err := bot.New(applicationConfig.TelegramBotToken,
		bot.WithDefaultHandler(messageHandler.HandleMessage),
	)
	if err != nil {
		logger.Error("failed to create telegram bot", "error", err)
		return fmt.Errorf("create Telegram bot: %w", err)
	}
	dealTopicService.SetTelegramForumTopicCreator(telegramBot)

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

// initializeAndRegisterTool logs each construction and registration phase for
// one native tool without exposing its configuration or runtime arguments.
func initializeAndRegisterTool(logger *slog.Logger, toolRegistry *applicationTools.Registry, toolName string, toolSource string, toolFactory func() (applicationTools.Tool, error)) error {
	initializationStartedAt := time.Now()
	logger.Info(fmt.Sprintf("initializing tool %s", toolName),
		"tool_name", toolName,
		"tool_source", toolSource,
		"phase", "initialize",
		"status", "initializing",
	)

	initializedTool, err := toolFactory()
	if err != nil {
		logger.Error(fmt.Sprintf("tool %s initialization failed", toolName),
			"tool_name", toolName,
			"tool_source", toolSource,
			"phase", "initialize",
			"duration_ms", time.Since(initializationStartedAt).Milliseconds(),
			"status", "failed",
			"error", err,
		)
		return err
	}

	logger.Info(fmt.Sprintf("registering tool %s", toolName),
		"tool_name", toolName,
		"tool_source", toolSource,
		"phase", "register",
		"status", "registering",
	)
	if err := toolRegistry.Register(initializedTool); err != nil {
		logger.Error(fmt.Sprintf("tool %s registration failed", toolName),
			"tool_name", toolName,
			"tool_source", toolSource,
			"phase", "register",
			"duration_ms", time.Since(initializationStartedAt).Milliseconds(),
			"status", "failed",
			"error", err,
		)
		return err
	}

	logger.Info(fmt.Sprintf("tool %s initialized and registered", toolName),
		"tool_name", toolName,
		"tool_source", toolSource,
		"duration_ms", time.Since(initializationStartedAt).Milliseconds(),
		"status", "ready",
	)
	return nil
}

// initializeMCPServers connects enabled Streamable HTTP servers independently.
// A failed server is logged and skipped so other tools remain available.
func initializeMCPServers(applicationContext context.Context, logger *slog.Logger, serverConfigurations []config.MCPServerConfig, toolRegistry *applicationTools.Registry) []*mcpclient.Connection {
	mcpConnections := make([]*mcpclient.Connection, 0, len(serverConfigurations))
	configuredMCPServerNames := make([]string, 0, len(serverConfigurations))
	enabledMCPServerNames := make([]string, 0, len(serverConfigurations))
	connectedMCPServerNames := make([]string, 0, len(serverConfigurations))
	for _, serverConfiguration := range serverConfigurations {
		configuredMCPServerNames = append(configuredMCPServerNames, serverConfiguration.Name)
		if serverConfiguration.Enabled {
			enabledMCPServerNames = append(enabledMCPServerNames, serverConfiguration.Name)
		}
	}
	logger.Info("MCP tool discovery started",
		"configured_mcp_servers", configuredMCPServerNames,
		"enabled_mcp_servers", enabledMCPServerNames,
		"status", "initializing",
	)
	discoveredMCPToolNames := make([]string, 0)
	for _, serverConfiguration := range serverConfigurations {
		if !serverConfiguration.Enabled {
			logger.Info(fmt.Sprintf("MCP %s is disabled", serverConfiguration.Name),
				"mcp_name", serverConfiguration.Name,
				"status", "disabled",
				"reason", "disabled by configuration",
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
		discoveredToolNames := make([]string, 0, len(discoveredTools))
		for _, discoveredTool := range discoveredTools {
			discoveredToolNames = append(discoveredToolNames, discoveredTool.Definition().Name)
		}
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
		connectedMCPServerNames = append(connectedMCPServerNames, serverConfiguration.Name)
		discoveredMCPToolNames = append(discoveredMCPToolNames, discoveredToolNames...)
		logger.Info(fmt.Sprintf("MCP %s loaded %d tools in %s", serverConfiguration.Name, len(discoveredTools), initializationDuration),
			"mcp_name", serverConfiguration.Name,
			"mcp_url", serverConfiguration.URL,
			"tool_count", len(discoveredTools),
			"tool_names", discoveredToolNames,
			"duration_ms", initializationDuration.Milliseconds(),
			"status", "ready",
		)
	}
	logger.Info("MCP tool discovery completed",
		"configured_server_count", len(serverConfigurations),
		"enabled_server_count", len(enabledMCPServerNames),
		"connected_server_count", len(mcpConnections),
		"connected_mcp_servers", connectedMCPServerNames,
		"discovered_tool_count", len(discoveredMCPToolNames),
		"discovered_tools", discoveredMCPToolNames,
		"status", "completed",
	)
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
