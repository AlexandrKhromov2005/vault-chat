// Command chat-load is the generator of the Chat/Web load acceptance. It is
// started by tests/load/chat_load.py inside the disposable stand and speaks
// line-delimited JSON on stdin/stdout (internal/chatload.StdioControl);
// progress goes to stderr. Exit codes: 0 gates passed, 3 a gate failed,
// 1 the run could not be completed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/AlexandrKhromov2005/vault-chat/internal/chatload"
)

const exitGateFailed = 3

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "chat-load:", err)
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	flags := flag.NewFlagSet("chat-load", flag.ContinueOnError)
	gateway := flags.String("gateway", "", "Gateway base URL, such as https://gateway:8080")
	metrics := flags.String("metrics", "", "service=URL pairs of the metrics listeners of auth, chat and gateway, comma-separated")
	profilePath := flags.String("profile", "", "profile JSON; the committed internal/chatload/profile.json by default")
	if err := flags.Parse(args); err != nil {
		return 2, err
	}
	if *gateway == "" || flags.NArg() > 0 {
		return 2, errors.New("usage: chat-load -gateway URL -metrics auth=URL,chat=URL,gateway=URL [-profile FILE]")
	}
	targets := map[string]string{}
	for _, pair := range strings.Split(*metrics, ",") {
		name, url, ok := strings.Cut(pair, "=")
		if !ok || name == "" || url == "" {
			return 2, fmt.Errorf("malformed -metrics entry %q", pair)
		}
		targets[name] = url
	}
	for _, service := range []string{"auth", "chat", "gateway"} {
		if targets[service] == "" {
			return 2, fmt.Errorf("-metrics needs %s", service)
		}
	}
	profile, err := chatload.DefaultProfile()
	if *profilePath != "" {
		data, readErr := os.ReadFile(*profilePath)
		if readErr != nil {
			return 1, readErr
		}
		profile, err = chatload.ParseProfile(data)
	}
	if err != nil {
		return 1, err
	}

	control := chatload.NewStdioControl(os.Stdin, os.Stdout)
	init, err := control.ReadInit()
	if err != nil {
		return 1, err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runner, err := chatload.NewRunner(chatload.Config{
		Profile: profile, GatewayURL: *gateway, Metrics: targets, Invitations: init.Invitations,
		Control: control, Logger: logger,
	})
	if err != nil {
		return 1, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, runErr := runner.Run(ctx)
	report.Stand = init.Stand
	if err := control.Emit(map[string]any{"event": "report", "report": report, "error": errorText(runErr)}); err != nil {
		return 1, err
	}
	switch {
	case runErr != nil:
		return 1, runErr
	case !report.Passed:
		for _, v := range report.Violations {
			logger.Error("gate failed", "violation", v.String())
		}
		return exitGateFailed, nil
	}
	return 0, nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
