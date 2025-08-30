package discordcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/alice-bnuy/errutil"
	"github.com/alice-bnuy/logutil"

	"github.com/bwmarrin/discordgo"
)

// newConfigManagerWithPaths creates a new configuration manager with separate config and cache paths.
func newConfigManager(configPath string) (*ConfigManager, error) {
	if configPath == "" {
		return nil, fmt.Errorf("config path cannot be empty")
	}

	// Ensure config directory exists
	if err := createDirectory(configPath); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	configFilePath := filepath.Join(configPath, ConfigFileName)

	return &ConfigManager{
		configFilePath: configFilePath,
		logsDirPath:    logutil.LogsDirPath,
		configPath:     configPath,
	}, nil
}

// Load loads the configuration from file.
func (mgr *ConfigManager) LoadConfig() error {
	path := mgr.configFilePath

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logutil.Warnf(LogLoadConfigFileNotFound, path)
			mgr.mu.Lock()
			mgr.config = &BotConfig{Guilds: []GuildConfig{}}
			mgr.mu.Unlock()
			return nil
		}
		logutil.Errorf(LogLoadConfigFailedReadFile, path, err)
		return errutil.HandleConfigError("read", mgr.configFilePath, func() error { return err })
	}

	var config BotConfig
	if err := json.Unmarshal(data, &config); err != nil {
		logutil.Errorf(LogLoadConfigFailedUnmarshal, path, err)
		return errutil.HandleConfigError("unmarshal", mgr.configFilePath, func() error { return err })
	}

	// Validating the loaded configuration
	if len(config.Guilds) == 0 {
		logutil.Warnf(LogLoadConfigNoGuilds, path)
	}

	mgr.mu.Lock()
	mgr.config = &config
	mgr.mu.Unlock()
	logutil.Infof(LogLoadConfigSuccess, path)
	return nil
}

// Save saves the current configuration to file.
func (mgr *ConfigManager) SaveConfig() error {
	log.Println("SaveConfig called")
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	if mgr.config == nil {
		log.Println("SaveConfig: config is nil")
		logutil.Errorf(LogSaveConfigNilConfig)
		return errors.New(ErrCannotSaveNilConfig)
	}

	path := mgr.configFilePath

	data, err := json.MarshalIndent(mgr.config, "", "  ")
	if err != nil {
		log.Printf("SaveConfig: failed to marshal config: %v", err)
		logutil.Errorf(LogSaveConfigFailedMarshal, err)
		return fmt.Errorf(ErrFailedMarshalConfig, err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("SaveConfig: failed to write file: %v", err)
		logutil.Errorf(LogSaveConfigFailedWriteFile, path, err)
		return errutil.HandleConfigError("write", mgr.configFilePath, func() error { return err })
	}

	log.Printf("SaveConfig: successfully saved to %s", path)
	logutil.Infof(LogSaveConfigSuccess, path)
	return nil
}

// --- Getters ---

// GetConfigPath returns the config file path.
func (mgr *ConfigManager) ConfigPath() string { return mgr.configFilePath }

// GetConfig returns the current configuration.
func (mgr *ConfigManager) Config() *BotConfig {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.config
}

// HasGuilds checks if there are configured guilds.
func (mgr *ConfigManager) HasAnyGuilds() bool {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.config != nil && len(mgr.config.Guilds) > 0
}

// --- Guild Config Management ---

// GuildConfig returns the configuration for a specific guild.
func (mgr *ConfigManager) GuildConfig(guildID string) *GuildConfig {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if mgr.config == nil {
		return nil
	}
	for i := range mgr.config.Guilds {
		if mgr.config.Guilds[i].GuildID == guildID {
			return &mgr.config.Guilds[i]
		}
	}
	return nil
}

// addGuildConfig adds or replaces a guild configuration (private function).
func (mgr *ConfigManager) addGuildConfig(guildCfg GuildConfig) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.config == nil {
		mgr.config = &BotConfig{Guilds: []GuildConfig{}}
	}
	var guilds []GuildConfig
	for _, g := range mgr.config.Guilds {
		if g.GuildID != guildCfg.GuildID {
			guilds = append(guilds, g)
		}
	}
	mgr.config.Guilds = append(guilds, guildCfg)
	return nil
}

// AddGuildConfig adds or replaces a guild configuration.
// Deprecated: Use addGuildConfig (private) instead.
func (mgr *ConfigManager) AddGuildConfig(guildCfg GuildConfig) error {
	return mgr.addGuildConfig(guildCfg)
}

// RemoveGuildConfig removes a guild configuration.
func (mgr *ConfigManager) RemoveGuildConfig(guildID string) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.config == nil {
		return
	}
	var guilds []GuildConfig
	for _, g := range mgr.config.Guilds {
		if g.GuildID != guildID {
			guilds = append(guilds, g)
		}
	}
	mgr.config.Guilds = guilds
}

// --- Guild Detection & Addition ---

// detectGuilds automatically detects guilds where the bot is present (private function).
func (mgr *ConfigManager) detectGuilds(session *discordgo.Session) error {
	mgr.mu.Lock()
	if mgr.config == nil {
		mgr.config = &BotConfig{Guilds: []GuildConfig{}}
	}
	mgr.config.Guilds = []GuildConfig{}
	mgr.mu.Unlock()

	for _, g := range session.State.Guilds {
		fullGuild, err := session.Guild(g.ID)
		if err != nil {
			logutil.WithField("guildID", g.ID).Warnf(LogCouldNotFetchGuild, err)
			continue
		}

		channelID := FindSuitableChannel(session, g.ID)
		if channelID == "" {
			logutil.WithField("guildID", g.ID).Warnf(LogNoSuitableChannel, fullGuild.Name)
			continue
		}

		roles := FindAdminRoles(session, g.ID, fullGuild.OwnerID)
		guildCfg := GuildConfig{
			GuildID:          g.ID,
			CommandChannelID: channelID,
			UserLogChannelID: channelID,
			AllowedRoles:     roles,
		}
		mgr.mu.Lock()
		mgr.config.Guilds = append(mgr.config.Guilds, guildCfg)
		mgr.mu.Unlock()
		logutil.WithFields(map[string]interface{}{
			"guildName": fullGuild.Name,
			"guildID":   g.ID,
			"channelID": channelID,
		}).Info(LogGuildAdded)
	}
	return mgr.SaveConfig()
}

// AddGuildToConfig adds a new guild to the configuration.
func (mgr *ConfigManager) RegisterGuild(session *discordgo.Session, guildID string) error {
	// ensure config exists
	mgr.mu.RLock()
	cfgNil := mgr.config == nil
	mgr.mu.RUnlock()
	if cfgNil {
		mgr.mu.Lock()
		if mgr.config == nil {
			mgr.config = &BotConfig{Guilds: []GuildConfig{}}
		}
		mgr.mu.Unlock()
	} else {
		mgr.mu.RLock()
		for _, g := range mgr.config.Guilds {
			if g.GuildID == guildID {
				mgr.mu.RUnlock()
				logutil.WithField("guildID", guildID).Info(LogGuildAlreadyConfigured)
				return nil
			}
		}
		mgr.mu.RUnlock()
	}
	guild, err := session.Guild(guildID)
	if err != nil {
		return fmt.Errorf(ErrGuildInfoFetchMsg, guildID, err)
	}
	channelID := FindSuitableChannel(session, guildID)
	if channelID == "" {
		return fmt.Errorf(ErrNoSuitableChannelMsg, guild.Name)
	}
	roles := FindAdminRoles(session, guildID, guild.OwnerID)
	guildCfg := GuildConfig{
		GuildID:          guildID,
		CommandChannelID: channelID,
		UserLogChannelID: channelID,
		AllowedRoles:     roles,
	}
	mgr.mu.Lock()
	mgr.config.Guilds = append(mgr.config.Guilds, guildCfg)
	mgr.mu.Unlock()
	channelName := channelID
	if ch, err := session.Channel(channelID); err == nil {
		channelName = ch.Name
	}
	logutil.WithFields(map[string]interface{}{
		"guildName": guild.Name,
		"guildID":   guildID,
		"channel":   channelName,
	}).Info(LogGuildAdded)
	return mgr.SaveConfig()
}

// --- Utility & Logging ---

func FindSuitableChannel(session *discordgo.Session, guildID string) string {
	channels, err := session.GuildChannels(guildID)
	if err != nil {
		return ""
	}
	for _, channel := range channels {
		if channel.Type == discordgo.ChannelTypeGuildText {
			permissions, err := session.UserChannelPermissions(session.State.User.ID, channel.ID)
			if err == nil && (permissions&discordgo.PermissionSendMessages) != 0 {
				if channel.Name == "general" || channel.Name == "geral" || channel.Name == "bot-logs" || channel.Name == "logs" {
					return channel.ID
				}
				if channel.ID != "" {
					return channel.ID
				}
			}
		}
	}
	return ""
}

func FindAdminRoles(session *discordgo.Session, guildID, ownerID string) []string {
	var allowedRoles []string
	roles, err := session.GuildRoles(guildID)
	if err == nil {
		for _, role := range roles {
			if role.Name != "@everyone" && (role.Permissions&discordgo.PermissionAdministrator) != 0 {
				allowedRoles = append(allowedRoles, role.ID)
			}
		}
	}
	if len(allowedRoles) == 0 && ownerID != "" {
		if member, err := session.GuildMember(guildID, ownerID); err == nil && len(member.Roles) > 0 {
			allowedRoles = append(allowedRoles, member.Roles[0])
		}
	}
	return allowedRoles
}

func GetTextChannels(session *discordgo.Session, guildID string) ([]*discordgo.Channel, error) {
	channels, err := session.GuildChannels(guildID)
	if err != nil {
		return nil, err
	}
	var textChannels []*discordgo.Channel
	for _, channel := range channels {
		if channel.Type == discordgo.ChannelTypeGuildText {
			permissions, err := session.UserChannelPermissions(session.State.User.ID, channel.ID)
			if err == nil && (permissions&discordgo.PermissionSendMessages) != 0 {
				textChannels = append(textChannels, channel)
			}
		}
	}
	return textChannels, nil
}

func ValidateChannel(session *discordgo.Session, guildID, channelID string) error {
	channel, err := session.Channel(channelID)
	if err != nil {
		return fmt.Errorf("%s: %w", ErrChannelNotFound, err)
	}
	if channel.GuildID != guildID {
		return errors.New(ErrChannelWrongGuild)
	}
	if channel.Type != discordgo.ChannelTypeGuildText {
		return errors.New(ErrChannelWrongType)
	}
	permissions, err := session.UserChannelPermissions(session.State.User.ID, channelID)
	if err != nil {
		return fmt.Errorf(ErrFailedCheckPerms, err)
	}
	if (permissions & discordgo.PermissionSendMessages) == 0 {
		return errors.New(ErrChannelNoPermissions)
	}
	return nil
}

func EnsureConfigFiles(configPath string) error {
	// Sanitize config path

	// Create config directory if it doesn't exist
	if err := ensureDirectories(configPath); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Check if config file exists
	configFilePath := filepath.Join(configPath, "config.json")
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		logutil.Infof("Config file not found, creating default at %s", configFilePath)

		// Create basic empty config
		defaultConfig := BotConfig{
			Guilds: []GuildConfig{},
		}
		configData, err := json.MarshalIndent(defaultConfig, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to create config file: %w", err)
		}
		if err := os.WriteFile(configFilePath, configData, 0666); err != nil {
			logutil.Errorf("Failed to write config file at %s: %v", configFilePath, err)
			return fmt.Errorf("failed to write config file: %w", err)
		}
		logutil.Infof("Config file successfully written at %s", configFilePath)
	}

	// Check if cache file exists
	cacheFilePath := filepath.Join(configPath, "cache.json")
	if _, err := os.Stat(cacheFilePath); os.IsNotExist(err) {
		logutil.Infof("Cache file not found, creating default at %s", cacheFilePath)

		// Create basic empty cache
		defaultCache := `{"guilds":{},"last_updated":"","version":"1.0"}`
		if err := os.WriteFile(cacheFilePath, []byte(defaultCache), 0666); err != nil {
			logutil.Errorf("Failed to write cache file at %s: %v", cacheFilePath, err)
			return fmt.Errorf("failed to write cache file: %w", err)
		}
		logutil.Infof("Cache file successfully written at %s", cacheFilePath)
	}

	return nil
}

// FindRulesetByID searches for a ruleset by its ID in the guild configuration.
func (cfg *GuildConfig) FindRulesetByID(id string) (*Ruleset, int) {
	for idx, rs := range cfg.Rulesets {
		if rs.ID == id {
			return &cfg.Rulesets[idx], idx
		}
	}
	return nil, -1
}
