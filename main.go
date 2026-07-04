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
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

const (
	svcName          = "dns-controller"
	shortDescription = "DNS Controller for managing DNS records"
	longDescription  = `DNS Controller is a tool for managing DNS records through various providers like Cloudflare.`
)

var (
	version = "development"

	command = &cobra.Command{
		Use:   svcName,
		Short: shortDescription,
		Long:  longDescription,
		RunE:  run,
	}

	cfgFile         string
	cloudflareKey   string
	cloudflareEmail string
	cloudflareAuth  string
)

func init() {
	command.AddCommand(newVersionCommand())

	command.Flags().StringVar(&cfgFile, "config", "config.yaml", "config file containing DNS records")
	command.Flags().StringVar(&cloudflareKey, "cloudflare-key", "", "Cloudflare API key")
	command.Flags().StringVar(&cloudflareEmail, "cloudflare-email", "fernbaughj@gmail.com", "Cloudflare account email")
	command.Flags().StringVar(&cloudflareAuth, "cloudflare-auth-type", "token", "Cloudflare auth type: 'token' or 'key' (default inferred)")

	_ = viper.BindPFlags(command.Flags())
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the dns-controller version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	}
}

func validateConfig() error {
	if cloudflareKey == "" {
		return errors.New("cloudflare API key/token not provided")
	}
	authType := strings.ToLower(strings.TrimSpace(cloudflareAuth))
	if authType != "token" && cloudflareEmail == "" {
		return errors.New("cloudflare email not provided when auth type is not token")
	}
	return nil
}

func run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	if err := validateConfig(); err != nil {
		return err
	}

	logStartup()

	records, err := readCfg()
	if err != nil {
		return errors.Join(err, errors.New("failed to read config"))
	}

	ctrl, err := controller.NewController(cloudflareKey, cloudflareEmail, cloudflareAuth)
	if err != nil {
		return err
	}

	if err = ctrl.Handle(ctx, records); err != nil {
		return errors.Join(err, errors.New("failed to check DNS record"))
	}

	return nil
}

func logStartup() {
	zap.L().Info("DNS Controller starting...", zap.String("version", version))
}

func readCfg() (controller.Config, error) {
	v := viper.New()
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		return controller.Config{}, errors.New("config file path is required")
	}

	if err := v.ReadInConfig(); err != nil {
		return controller.Config{}, fmt.Errorf("failed to read config file %s: %w", cfgFile, err)
	}

	zap.L().Info("Using config file", zap.String("file", v.ConfigFileUsed()))

	var config controller.Config
	if err := v.Unmarshal(&config); err != nil {
		return controller.Config{}, fmt.Errorf("unable to decode config into struct: %w", err)
	}

	zap.L().Info("Configuration loaded", zap.Int("records_count", len(config.Records)))
	for _, record := range config.Records {
		zap.L().Debug("Loaded record",
			zap.String("id", record.ID),
			zap.String("type", record.RecordType),
			zap.String("name", record.Name))
	}

	zap.L().Debug("Cloudflare auth configured")
	if cloudflareEmail != "" {
		zap.L().Info("Cloudflare email configured", zap.String("email", cloudflareEmail))
	}

	return config, nil
}

func main() {
	if err := app.RunCobraCommand(context.Background(), command); err != nil {
		log.Fatal(err)
	}
}
