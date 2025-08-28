package discordcore

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// GuildConfig holds the configuration for a specific guild.
type GuildConfig struct {
	GuildID             string    `json:"guild_id"`
	CommandChannelID    string    `json:"command_channel_id"`
	UserLogChannelID    string    `json:"user_log_channel_id"` // Renamed from AvatarLogChannelID
	AutomodLogChannelID string    `json:"automod_log_channel_id"`
	AllowedRoles        []string  `json:"allowed_roles"`
	Rulesets            []Ruleset `json:"rulesets,omitempty"`
	LooseLists          []Rule    `json:"loose_rules,omitempty"` // Regras soltas, não associadas a nenhuma ruleset
	Blocklist           []string  `json:"blocklist,omitempty"`
}

// BotConfig holds the configuration for the bot.
type BotConfig struct {
	Guilds      []GuildConfig `json:"guilds"`
	ActiveGuild string        `json:"active_guild,omitempty"`
}

// Manager handles bot configuration management.
type ConfigManager struct {
	configFilePath string
	cacheFilePath  string
	logsDirPath    string
	configPath     string
	config         *BotConfig
	mu             sync.RWMutex
}

// AvatarCache holds the cached avatars for a guild.
type AvatarCache struct {
	Avatars     map[string]string `json:"avatars"`
	LastUpdated time.Time         `json:"last_updated"`
	GuildID     string            `json:"guild_id"`
}

// AvatarChange holds information about a user's avatar change.
type AvatarChange struct {
	UserID    string
	Username  string
	OldAvatar string
	NewAvatar string
	Timestamp time.Time
}

// RuleType distinguishes between native Discord rules and custom bot rules
const (
	RuleTypeNative = "native"
	RuleTypeCustom = "custom"
)

// List represents a single list in the system.
type List struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // "native" or "custom"
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Native list fields
	NativeID string `json:"native_id,omitempty"`
	// Custom list fields
	BlockedKeywords []string `json:"blocked_keywords,omitempty"`
}

// Rule representa uma regra que pode carregar um conjunto de listas.
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Lists   []List `json:"lists"` // Conjunto de listas associadas à regra
	Enabled bool   `json:"enabled"`
}

// Ruleset holds a collection of rules.
type Ruleset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Rules   []Rule `json:"rules"`
	Enabled bool   `json:"enabled"`
}

// StatusString returns a human-readable status for the ruleset (Enabled/Disabled)
func (r Ruleset) StatusString() string {
	if r.Enabled {
		return "Enabled"
	}
	return "Disabled"
}

// Adicionar métodos para gerenciar lists, rules e rulesets no ConfigManager
func (mgr *ConfigManager) AddList(guildID string, list List) error {
	log.Printf("AddList called with guildID: %s, listID: %s", guildID, list.ID)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	guildConfig := mgr.GuildConfig(guildID)
	if guildConfig == nil {
		log.Printf("GuildConfig not found for guildID: %s", guildID)
		return fmt.Errorf("guild not found")
	}

	log.Printf("Appending list to LooseLists for guildID: %s", guildID)
	guildConfig.LooseLists = append(guildConfig.LooseLists, Rule{
		ID:      list.ID,
		Name:    list.Name,
		Lists:   []List{list},
		Enabled: true,
	})
	log.Printf("List appended successfully for guildID: %s", guildID)
	return mgr.SaveConfig()
}

func (mgr *ConfigManager) AddRule(guildID string, rule Rule) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	guildConfig := mgr.GuildConfig(guildID)
	if guildConfig == nil {
		return fmt.Errorf("guild not found")
	}

	guildConfig.LooseLists = append(guildConfig.LooseLists, rule)
	return mgr.SaveConfig()
}

func (mgr *ConfigManager) AddRuleset(guildID string, ruleset Ruleset) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	guildConfig := mgr.GuildConfig(guildID)
	if guildConfig == nil {
		return fmt.Errorf("guild not found")
	}

	guildConfig.Rulesets = append(guildConfig.Rulesets, ruleset)
	return mgr.SaveConfig()
}

// Corrigir lógica para adicionar lists a rules
func (mgr *ConfigManager) AddListToRule(guildID string, ruleID string, list List) error {
	log.Printf("AddListToRule called with guildID: %s, ruleID: %s, listID: %s", guildID, ruleID, list.ID)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	guildConfig := mgr.GuildConfig(guildID)
	if guildConfig == nil {
		log.Printf("GuildConfig not found for guildID: %s", guildID)
		return fmt.Errorf("guild not found")
	}

	for i, rule := range guildConfig.LooseLists {
		if rule.ID == ruleID {
			log.Printf("Rule found for ruleID: %s, appending list", ruleID)
			guildConfig.LooseLists[i].Lists = append(guildConfig.LooseLists[i].Lists, list)
			log.Printf("List appended successfully to ruleID: %s", ruleID)
			return mgr.SaveConfig()
		}
	}

	log.Printf("Rule not found for ruleID: %s", ruleID)
	return fmt.Errorf("rule not found")
}

// FindListByName searches for a list by its name in LooseLists.
func (gc *GuildConfig) FindListByName(name string) *List {
	for _, rule := range gc.LooseLists {
		for _, list := range rule.Lists {
			if list.Name == name {
				return &list
			}
		}
	}
	return nil
}

// ListExists checks if a list with the given name already exists in LooseLists.
func (gc *GuildConfig) ListExists(name string) bool {
	return gc.FindListByName(name) != nil
}

// DiscordCore holds the core configuration for the Discord bot package.
type DiscordCore struct {
	BotName       string
	Token         string
	SupportPath   string
	ConfigPath    string
	Session       *discordgo.Session
	ConfigManager *ConfigManager
}

// NewDiscordCore creates a new DiscordCore instance.
// It takes a Discord bot token and initializes the core with configuration management.
//
// Example usage:
//
//	core, err := discordcore.NewDiscordCore("YOUR_BOT_TOKEN_HERE")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	session, err := core.NewDiscordSession()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Bot is now ready to use
//	defer session.Close()
func NewDiscordCore(token string) (*DiscordCore, error) {
	if token == "" {
		return nil, fmt.Errorf("discord bot token cannot be empty")
	}

	// Get bot name from Discord API using the token
	botName, err := getBotNameFromAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to get bot name from API: %w", err)
	}

	supportPath := getApplicationSupportPath(botName)
	configPath := filepath.Join(supportPath, "data")

	// Ensure directories exist
	if err := ensureDirectories([]string{supportPath, configPath}); err != nil {
		return nil, fmt.Errorf("failed to ensure directories: %w", err)
	}

	// Ensure config files exist
	if err := EnsureConfigFiles(configPath); err != nil {
		return nil, fmt.Errorf("failed to ensure config files: %w", err)
	}

	configManager, err := newConfigManager(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create config manager: %w", err)
	}

	return &DiscordCore{
		BotName:       botName,
		Token:         token,
		SupportPath:   supportPath,
		ConfigPath:    configPath,
		ConfigManager: configManager,
	}, nil
}

// NewConfigManager creates a new ConfigManager using this DiscordCore's config path.
func (core *DiscordCore) NewConfigManager() (*ConfigManager, error) {
	return newConfigManager(core.ConfigPath)
}

// NewAvatarCacheManager creates a new AvatarCacheManager using this DiscordCore's config path.
func (core *DiscordCore) NewAvatarCacheManager() (*AvatarCacheManager, error) {
	return newAvatarCacheManager(core.ConfigPath)
}

// GetToken returns the Discord bot token.
func (core *DiscordCore) GetToken() string {
	return core.Token
}

// GetBotName returns the bot name.
func (core *DiscordCore) GetBotName() string {
	return core.BotName
}

// GetConfigPath returns the config path.
func (core *DiscordCore) GetConfigPath() string {
	return core.ConfigPath
}

// GetSupportPath returns the support path.
func (core *DiscordCore) GetSupportPath() string {
	return core.SupportPath
}

// GetSession returns the Discord session.
func (core *DiscordCore) GetSession() *discordgo.Session {
	return core.Session
}

// DetectGuilds detects guilds where the bot is present and adds them to the config.
func (core *DiscordCore) DetectGuilds() error {
	return core.ConfigManager.DetectGuilds(core.Session)
}

// RegisterGuild adds a new guild to the configuration.
func (core *DiscordCore) RegisterGuild(guildID string) error {
	return core.ConfigManager.RegisterGuild(core.Session, guildID)
}

// ShowConfiguredGuilds logs the configured guilds.
func (core *DiscordCore) ShowConfiguredGuilds() {
	ShowConfiguredGuilds(core.Session, core.ConfigManager)
}

// LogConfiguredGuilds logs a summary of configured guilds.
func (core *DiscordCore) LogConfiguredGuilds() error {
	return LogConfiguredGuilds(core.ConfigManager, core.Session)
}

// getBotNameFromAPI fetches the bot's username from the Discord API using the token.
func getBotNameFromAPI(token string) (string, error) {
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for bot info: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch bot info from Discord API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord API returned status %d when fetching bot info", resp.StatusCode)
	}

	var user struct {
		Username string `json:"username"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", fmt.Errorf("failed to decode bot info response: %w", err)
	}

	log.Printf("Fetched bot name from API: %s", user.Username)
	return user.Username, nil
}

// getApplicationSupportPath returns the application support path based on bot name.
func getApplicationSupportPath(botName string) string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", botName)
}
