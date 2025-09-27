package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alice-bnuy/discordcore/pkg/discord/commands"
	"github.com/alice-bnuy/discordcore/pkg/discord/commands/admin"
	"github.com/alice-bnuy/discordcore/pkg/discord/logging"
	"github.com/alice-bnuy/discordcore/pkg/discord/session"
	"github.com/alice-bnuy/discordcore/pkg/errors"
	"github.com/alice-bnuy/discordcore/pkg/errutil"
	"github.com/alice-bnuy/discordcore/pkg/files"
	"github.com/alice-bnuy/discordcore/pkg/log"
	"github.com/alice-bnuy/discordcore/pkg/service"
	"github.com/alice-bnuy/discordcore/pkg/storage"
	"github.com/alice-bnuy/discordcore/pkg/task"
	"github.com/alice-bnuy/discordcore/pkg/util"
)

// main is the entry point of the Discord bot.
func main() {
	// Load environment with fallback search under $HOME/.local/bin.
	// Use the shared util function so other repositories can reuse the same logic.
	var loadErr error
	var token string
	token, loadErr = util.LoadEnvWithLocalBinFallback("ALICE_BOT_DEVELOPMENT_TOKEN")
	if loadErr != nil {
		// Keep the original single-line Portuguese message for parity with previous behavior.
	// Initialize global logger
	if err := log.SetupLogger(); err != nil {
		fmt.Printf("failed to configure logger: %v\n", err)
		os.Exit(1)
	}

	// Initialize global error handler
	if err := errutil.InitializeGlobalErrorHandler(log.GlobalLogger); err != nil {
		fmt.Fprintln(os.Stderr, "failed to initialize error handler:", err)
		os.Exit(1)
	}

	// Initialize unified error handler
	errorHandler := errors.NewErrorHandler()

	// Log bot startup
	log.Info(log.Application, "🚀 Starting bot...")

	// Ensure token present (already loaded by util.LoadEnvWithLocalBinFallback)
	if token == "" {
		log.Errorf("Discord bot token (ALICE_BOT_DEVELOPMENT_TOKEN) is not set in environment")
		os.Exit(1)
	}

	// Config manager will be initialized after bot name is set (paths correct)

	// Add detailed logging for Discord authentication
	log.Info(log.DiscordEvents, "🔑 Attempting to authenticate with Discord API...")
	log.Info(log.DiscordEvents, "Using bot token from ALICE_BOT_DEVELOPMENT_TOKEN environment variable (token redacted)")

	// Create Discord session and ensure safe shutdown
	discordSession, err := session.NewDiscordSession(token)
	if err != nil {
		log.Errorf("❌ Authentication failed with Discord API: %v", err)
		log.Errorf("❌ Error creating Discord session: %v", err)
		os.Exit(1)
	}
	log.Infof(log.DiscordEvents, "✅ Successfully authenticated with Discord API as %s#%s", discordSession.State.User.Username, discordSession.State.User.Discriminator)

	// Set bot name from Discord and recompute app support path
	util.SetBotName(discordSession.State.User.Username)

	// Ensure cache directories exist for new caches root
	if err := util.EnsureCacheDirs(); err != nil {
		log.Errorf("Failed to create cache directories: %v", err)
		log.Error("❌ Failed to create cache directories")
		os.Exit(1)
	}

	// Ensure config and cache files exist (now using the right bot name path)
	if err := files.EnsureConfigFiles(); err != nil {
		log.Errorf("Error checking config files: %v", err)
		log.Error("❌ Error checking config files")
		os.Exit(1)
	}

	// Initialize config manager (uses the right path now)
	configManager := files.NewConfigManager()
	// Load existing settings from disk before starting services
	if err := configManager.LoadConfig(); err != nil {
		log.Errorf("Failed to load settings file: %v", err)
	}

	// One-time migration: move JSON avatar cache into SQLite and remove JSON files
	if err := util.MigrateAvatarJSONToSQLite(); err != nil {
		log.Errorf("Failed to migrate avatar JSON cache to SQLite (continuing): %v", err)
	}

	// Initialize SQLite store (messages, avatars, joins)
	store := storage.NewStore(util.GetMessageDBPath())
	if err := store.Init(); err != nil {
		log.Errorf("Failed to initialize SQLite store: %v", err)
		log.Error("❌ Failed to initialize SQLite store")
		os.Exit(1)
	}

	// Log summary of configured guilds
	if err := files.LogConfiguredGuilds(configManager, discordSession); err != nil {
		log.Errorf("Some configured guilds could not be accessed: %v", err)
	}

	// Downtime-aware silent avatar refresh before starting services/notifications
	if store != nil {
		if lastHB, ok, err := store.GetHeartbeat(); err == nil {
			if !ok || time.Since(lastHB) > 30*time.Minute {
				log.Info(log.Application, "⏱️ Detected downtime > 30m; performing silent avatar refresh before enabling notifications")
				if cfg := configManager.Config(); cfg != nil {
					for _, gcfg := range cfg.Guilds {
						members, err := discordSession.GuildMembers(gcfg.GuildID, "", 1000)
						if err != nil {
							log.Errorf("Failed to list members for silent refresh for guild %s: %v", gcfg.GuildID, err)
							continue
						}
						for _, member := range members {
							if member == nil || member.User == nil {
								continue
							}
							avatarHash := member.User.Avatar
							if avatarHash == "" {
								avatarHash = "default"
							}
							_, _, _ = store.UpsertAvatar(gcfg.GuildID, member.User.ID, avatarHash, time.Now())
						}
					}
				}
				log.Info(log.Application, "✅ Silent avatar refresh completed")
			} else {
				log.Info(log.Application, "No significant downtime detected; skipping silent avatar refresh")
			}
		} else {
			log.Errorf("Failed to read last heartbeat; skipping downtime check: %v", err)
		}
		_ = store.SetHeartbeat(time.Now())
	}

	// Initialize Service Manager
	serviceManager := service.NewServiceManager(errorHandler)

	// Create service wrappers for existing services
	log.Info(log.Application, "🔧 Creating service wrappers...")

	// Wrap MonitoringService
	monitoringService, err := logging.NewMonitoringService(discordSession, configManager, store)
	if err != nil {
		log.Errorf("Failed to create monitoring service: %v", err)
		log.Error("❌ Failed to create monitoring service")
		os.Exit(1)
	}

	monitoringWrapper := service.NewServiceWrapper(
		"monitoring",
		service.TypeMonitoring,
		service.PriorityHigh,
		[]string{}, // No dependencies
		func() error { return monitoringService.Start() },
		func() error { return monitoringService.Stop() },
		func() bool { return true }, // Simple health check
	)

	// Wrap AutomodService
	automodService := logging.NewAutomodService(discordSession, configManager)
	// Wire Automod with TaskRouter via NotificationAdapters (uses same notifier/config/cache)
	automodRouter := task.NewRouter(task.Defaults())
	automodAdapters := task.NewNotificationAdapters(automodRouter, discordSession, configManager, store, monitoringService.Notifier())
	automodService.SetAdapters(automodAdapters)
	automodWrapper := service.NewServiceWrapper(
		"automod",
		service.TypeAutomod,
		service.PriorityNormal,
		[]string{}, // No dependencies
		func() error { automodService.Start(); return nil },
		func() error { automodService.Stop(); return nil },
		func() bool { return true }, // Simple health check
	)

	// Register services with the manager
	if err := serviceManager.Register(monitoringWrapper); err != nil {
		log.Errorf("Failed to register monitoring service: %v", err)
		log.Error("❌ Failed to register monitoring service")
		os.Exit(1)
	}

	if err := serviceManager.Register(automodWrapper); err != nil {
		log.Errorf("Failed to register automod service: %v", err)
		log.Error("❌ Failed to register automod service")
		os.Exit(1)
	}

	// Start all services
	log.Info(log.Application, "🚀 Starting all services...")
	if err := serviceManager.StartAll(); err != nil {
		log.Errorf("Failed to start services: %v", err)
		log.Error("❌ Failed to start services")
		os.Exit(1)
	}

	// Initialize and register bot commands
	commandHandler := commands.NewCommandHandler(discordSession, configManager)
	if err := commandHandler.SetupCommands(); err != nil {
		log.Errorf("Error configuring slash commands: %v", err)
		log.Error("❌ Error configuring slash commands")
		os.Exit(1)
	}

	// Register admin commands
	adminCommands := admin.NewAdminCommands(serviceManager)
	adminCommands.RegisterCommands(commandHandler.GetCommandManager().GetRouter())

	// Ensure safe shutdown of all services
	defer func() {
		log.Info(log.Application, "🛑 Shutting down services...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := serviceManager.StopAll(); err != nil {
			log.Errorf("Some services failed to stop cleanly: %v", err)
		}
		if store != nil {
			_ = store.Close()
		}
		_ = shutdownCtx // Avoid unused variable warning
	}()

	// Log successful initialization and wait for shutdown
	log.Info(log.Application, "🔗 Slash commands sync completed")
	log.Info(log.Application, "🎯 Bot initialized successfully!")
	log.Info(log.Application, "🤖 Bot running. Monitoring all configured guilds. Press Ctrl+C to stop...")

	util.WaitForInterrupt()
	log.Info(log.Application, "🛑 Stopping bot...")
}
