package discordcore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/alice-bnuy/logutil"
)

// GetBotNameFromAPI fetches the bot's username from the Discord API using the token.
func GetBotNameFromAPI(token string) (string, error) {
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

	return user.Username, nil
}

// createDirectory ensures a directory exists by creating it and all necessary parent directories.
func createDirectory(path string) error {
	exists, err := checkIfDirectoryExists(path)
	if err != nil {
		return fmt.Errorf("failed to check if directory exists: %w", err)
	}
	if exists {
		return fmt.Errorf("directory already exists at %s", path)
	}
	if err := ensureDirectories(path); err != nil {
		logutil.Errorf("Failed to create directory %s: %v", path, err) // Log the error for debugging
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

func ensureDirectories(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		logutil.Errorf("Failed to create directory: %s, error: %v", path, err)
		return fmt.Errorf("failed to create directory: %w", err)
	}
	logutil.Infof("Directory created at %s", path)
	return nil
}

func checkIfDirectoryExists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		logutil.Infof("Directory already exists at %s", path)
		return true, nil
	} else if os.IsNotExist(err) {
		logutil.Warnf("Directory does not exist at %s", path)
		return false, nil
	}
	logutil.Errorf("Failed to check directory existence")
	return false, fmt.Errorf("failed to check directory existence")
}
