package cmd

import (
	"fmt"

	"github.com/pampatzoglou/k8s-capacity/pkg"
	"github.com/spf13/cobra"
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze resource usage and quotas",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := pkg.NewPrometheusClient(promURL, logger)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create Prometheus client")
		}

		if namespace == "" {
			logger.Fatal("Namespace is required")
		}

		result, err := client.QueryCPUUsageForNamespace(namespace)
		if err != nil {
			logger.WithError(err).Fatal("Error fetching CPU usage")
		}

		fmt.Printf("Analysis for namespace '%s': CPU usage %v\n", namespace, result)
	},
}

func init() {
	rootCmd.AddCommand(analyzeCmd)
}
