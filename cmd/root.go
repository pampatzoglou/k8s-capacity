package cmd

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile   string
	logger    *logrus.Logger
	promURL   string
	namespace string
)

var rootCmd = &cobra.Command{
	Use:   "k8s-capacity",
	Short: "Kubernetes CLI for resource recommendations and analysis",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		logger.WithError(err).Error("Command failed")
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is config.yaml)")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Namespace to use")
}

func initConfig() {
	logger = logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.InfoLevel)

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
	}

	if err := viper.ReadInConfig(); err != nil {
		logger.Fatalf("Error reading config file: %s", err)
	}

	promURL = viper.GetString("prometheus.url")
	if promURL == "" {
		logger.Fatal("Prometheus URL is not set in config")
	}

	logger.Info("Configuration loaded successfully")
}
