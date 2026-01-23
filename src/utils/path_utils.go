package utils

import (
	"fmt"
	"path"
	"strings"
	"time"
)

// RenderPathTemplate renders a path template with the given variables
// Supported variables: ${year}, ${month}, ${day}, ${room}, ${user}, ${file}
func RenderPathTemplate(template, roomName, user, filename string, eventTime time.Time) string {
	if eventTime.IsZero() {
		eventTime = time.Now()
	}

	replacements := map[string]string{
		"${year}":  fmt.Sprintf("%d", eventTime.Year()),
		"${month}": fmt.Sprintf("%02d", eventTime.Month()),
		"${day}":   fmt.Sprintf("%02d", eventTime.Day()),
		"${room}":  roomName,
		"${user}":  user,
		"${file}":  filename,
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return path.Clean(result)
}

func GenerateNextcloudPath(basePath, channel, user, filename string) string {
	year := time.Now().Year()
	return fmt.Sprintf("%s/%d/%s/%s/%s", basePath, year, channel, user, filename)
}

func SanitizePathSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, ":", "_")
	value = strings.ReplaceAll(value, "..", "_")
	if value == "" {
		return "unknown"
	}
	return value
}

func MatrixUserLocalpart(userID string) string {
	userID = strings.TrimSpace(userID)
	userID = strings.TrimPrefix(userID, "@")
	parts := strings.SplitN(userID, ":", 2)
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return userID
}

// RenderMessageTemplate renders a message template with the given variables
// Supported variables: ${user}, ${file}
func RenderMessageTemplate(template, user, filename string) string {
	replacements := map[string]string{
		"${user}": user,
		"${file}": filename,
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// GenerateNextcloudWebLink generates a direct Nextcloud web link to view a file
// Format: https://nextcloud.example.com/apps/files/?dir=/path&scrollto=filename
func GenerateNextcloudWebLink(webURL, filePath string) string {
	if webURL == "" {
		return ""
	}

	// Clean the file path and extract directory and filename
	cleanPath := path.Clean(filePath)
	dir := path.Dir(cleanPath)
	filename := path.Base(cleanPath)

	// Build the URL
	// Use /apps/files/?dir={directory}&scrollto={filename} format
	// This requires authentication and highlights the file in the directory
	webURL = strings.TrimRight(webURL, "/")
	return fmt.Sprintf("%s/apps/files/?dir=%s&scrollto=%s", webURL, dir, filename)
}
