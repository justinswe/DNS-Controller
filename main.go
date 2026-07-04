package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/justinswe/dns-controller/internal/controller"
	"github.com/justinswe/std/app"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const (
	serviceName      = "dns-controller"
	shortDescription = "DNS Controller for managing DNS records."
	longDescription  = `DNS Controller manages Cloudflare A records for networks without a static public IP address.`
)

var version = "development"

type config struct {
	records            []string
	cloudflareAPIToken string
	cloudflareAPIKey   string
	cloudflareEmail    string
}

func newCommand(buildVersion string) *cobra.Command {
	cfg := config{}
	command := &cobra.Command{
		Use:   serviceName,
		Short: shortDescription,
		Long:  longDescription,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return run(command, cfg, buildVersion)
		},
	}

	command.Flags().StringSliceVar(&cfg.records, "records", nil, "Cloudflare A record FQDNs to manage (comma-separated or repeatable)")
	command.Flags().StringVar(&cfg.cloudflareAPIToken, "cloudflare-api-token", "", "Cloudflare API token")
	command.Flags().StringVar(&cfg.cloudflareAPIKey, "cloudflare-api-key", "", "Cloudflare global API key")
	command.Flags().StringVar(&cfg.cloudflareEmail, "cloudflare-email", "", "Cloudflare account email (required with a global API key)")
	command.AddCommand(newVersionCommand(buildVersion))

	return command
}

func newVersionCommand(buildVersion string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the dns-controller version",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(command.OutOrStdout(), buildVersion)
			return err
		},
	}
}

func (cfg config) validated() (config, error) {
	if len(cfg.records) == 0 {
		return config{}, errors.New("at least one DNS record is required")
	}

	records := make([]string, len(cfg.records))
	for index, record := range cfg.records {
		records[index] = strings.TrimSpace(record)
		if records[index] == "" {
			return config{}, fmt.Errorf("DNS record at position %d is empty", index+1)
		}
	}

	cfg.records = records
	cfg.cloudflareAPIToken = strings.TrimSpace(cfg.cloudflareAPIToken)
	cfg.cloudflareAPIKey = strings.TrimSpace(cfg.cloudflareAPIKey)
	cfg.cloudflareEmail = strings.TrimSpace(cfg.cloudflareEmail)
	if err := cfg.cloudflareConfig().Validate(); err != nil {
		return config{}, err
	}

	return cfg, nil
}

func (cfg config) cloudflareConfig() controller.CloudflareConfig {
	return controller.CloudflareConfig{
		APIToken: cfg.cloudflareAPIToken,
		APIKey:   cfg.cloudflareAPIKey,
		Email:    cfg.cloudflareEmail,
	}
}

func run(command *cobra.Command, cfg config, buildVersion string) error {
	validatedConfig, err := cfg.validated()
	if err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}

	logStartup(buildVersion)
	zap.L().Info("Configuration loaded", zap.Int("records_count", len(validatedConfig.records)))

	ctrl, err := controller.NewController(validatedConfig.cloudflareConfig())
	if err != nil {
		return fmt.Errorf("create cloudflare controller: %w", err)
	}

	if err := ctrl.Handle(command.Context(), validatedConfig.records); err != nil {
		return fmt.Errorf("handle DNS records: %w", err)
	}

	return nil
}

func logStartup(buildVersion string) {
	zap.L().Info("DNS Controller starting", zap.String("version", buildVersion))
}

func main() {
	if err := app.RunCobraCommand(context.Background(), newCommand(version)); err != nil {
		log.Fatal(err)
	}
}
