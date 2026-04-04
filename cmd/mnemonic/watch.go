package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/gorilla/websocket"
)

// watchCommand connects to the daemon's WebSocket and streams live events.
func watchCommand(configPath string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		die(exitConfig, fmt.Sprintf("loading config: %v", err), "mnemonic diagnose")
	}

	wsURL := fmt.Sprintf("ws://%s:%d/ws", cfg.API.Host, cfg.API.Port)

	fmt.Printf("%sMnemonic Live Events%s — connecting to %s\n", colorBold, colorReset, wsURL)
	fmt.Printf("Press Ctrl+C to stop.\n\n")

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		die(exitNetwork, fmt.Sprintf("connecting to daemon: %v", err), "mnemonic start")
	}
	defer func() { _ = conn.Close() }()

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, shutdownSignals()...)

	go func() {
		<-sigChan
		fmt.Printf("\n%sStopping event watch.%s\n", colorGray, colorReset)
		_ = conn.Close()
		os.Exit(0)
	}()

	// Read and display events
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				fmt.Println("Connection closed.")
			} else {
				fmt.Fprintf(os.Stderr, "\nWebSocket disconnected: %v\n", err)
			}
			return
		}

		formatWatchEvent(message)
	}
}

// formatWatchEvent formats and prints a WebSocket event with colors.
func formatWatchEvent(data []byte) {
	var evt map[string]interface{}
	if err := json.Unmarshal(data, &evt); err != nil {
		// Raw text event
		ts := time.Now().Format("15:04:05")
		fmt.Printf("%s%s%s %s\n", colorGray, ts, colorReset, string(data))
		return
	}

	eventType, _ := evt["type"].(string)
	ts := time.Now().Format("15:04:05")

	switch eventType {
	case "raw_memory_created":
		source, _ := evt["source"].(string)
		id, _ := evt["id"].(string)
		shortID := truncID(id)
		fmt.Printf("%s%s%s %s▶ PERCEIVED%s [%s] %s\n",
			colorGray, ts, colorReset, colorCyan, colorReset, source, shortID)

	case "memory_encoded":
		id, _ := evt["id"].(string)
		shortID := truncID(id)
		fmt.Printf("%s%s%s %s▶ ENCODED%s   %s\n",
			colorGray, ts, colorReset, colorGreen, colorReset, shortID)

	case "consolidation_completed":
		processed, _ := evt["memories_processed"].(float64)
		decayed, _ := evt["memories_decayed"].(float64)
		merged, _ := evt["merged_clusters"].(float64)
		pruned, _ := evt["associations_pruned"].(float64)
		durationMs, _ := evt["duration_ms"].(float64)
		fmt.Printf("%s%s%s %s▶ CONSOLIDATED%s  processed=%d decayed=%d merged=%d pruned=%d (%dms)\n",
			colorGray, ts, colorReset, colorYellow, colorReset,
			int(processed), int(decayed), int(merged), int(pruned), int(durationMs))

	case "query_executed":
		query, _ := evt["query"].(string)
		results, _ := evt["result_count"].(float64)
		took, _ := evt["took_ms"].(float64)
		fmt.Printf("%s%s%s %s▶ QUERY%s      \"%s\" → %d results (%dms)\n",
			colorGray, ts, colorReset, colorBlue, colorReset,
			query, int(results), int(took))

	case "dream_cycle_completed":
		replayed, _ := evt["memories_replayed"].(float64)
		strengthened, _ := evt["associations_strengthened"].(float64)
		newAssoc, _ := evt["new_associations_created"].(float64)
		demoted, _ := evt["noisy_memories_demoted"].(float64)
		durationMs, _ := evt["duration_ms"].(float64)
		fmt.Printf("%s%s%s %s▶ DREAMED%s    replayed=%d strengthened=%d new_assoc=%d demoted=%d (%dms)\n",
			colorGray, ts, colorReset, colorCyan, colorReset,
			int(replayed), int(strengthened), int(newAssoc), int(demoted), int(durationMs))

	case "meta_cycle_completed":
		observations, _ := evt["observations_logged"].(float64)
		fmt.Printf("%s%s%s %s▶ META%s       observations=%d\n",
			colorGray, ts, colorReset, colorCyan, colorReset, int(observations))

	default:
		// Generic event
		fmt.Printf("%s%s%s %s▶ %s%s  %s\n",
			colorGray, ts, colorReset, colorGray, eventType, colorReset,
			string(data))
	}
}

// truncID shortens a UUID for display.
func truncID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
