package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// insightsCommand displays recent metacognition observations.
func insightsCommand(configPath string) {
	_, db, _, _ := initRuntime(configPath)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	observations, err := db.ListMetaObservations(ctx, "", 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching insights: %v\n", err)
		os.Exit(1)
	}

	if len(observations) == 0 {
		fmt.Println("No insights available yet. The metacognition agent runs periodically to analyze memory health.")
		fmt.Println("Run manually with: mnemonic meta-cycle")
		return
	}

	fmt.Printf("%sMnemonic Insights%s\n\n", colorBold, colorReset)

	for _, obs := range observations {
		// Severity color
		severityColor := colorGray
		switch obs.Severity {
		case "warning":
			severityColor = colorYellow
		case "critical":
			severityColor = colorRed
		case "info":
			severityColor = colorCyan
		}

		// Format observation type
		typeLabel := strings.ReplaceAll(obs.ObservationType, "_", " ")
		typeLabel = strings.ToUpper(typeLabel[:1]) + typeLabel[1:]

		ago := time.Since(obs.CreatedAt).Round(time.Minute)
		timeStr := formatDuration(ago)
		if timeStr != "just now" {
			timeStr += " ago"
		}
		fmt.Printf("  %s[%s]%s %s%s%s (%s)\n",
			severityColor, strings.ToUpper(obs.Severity), colorReset,
			colorBold, typeLabel, colorReset,
			timeStr)

		// Print details
		for key, val := range obs.Details {
			keyLabel := strings.ReplaceAll(key, "_", " ")
			fmt.Printf("    %s: %s\n", keyLabel, formatDetailValue(val))
		}
		fmt.Println()
	}
}

// formatDetailValue renders a detail value in a human-friendly way.
func formatDetailValue(val interface{}) string {
	switch v := val.(type) {
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%.1f%%", v*100)
	case map[string]interface{}:
		parts := []string{}
		for k, mv := range v {
			switch n := mv.(type) {
			case float64:
				parts = append(parts, fmt.Sprintf("%s=%d", k, int64(n)))
			default:
				parts = append(parts, fmt.Sprintf("%s=%v", k, mv))
			}
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", val)
	}
}
