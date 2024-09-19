package cmd

import (
	"fmt"

	"github.com/pampatzoglou/k8s-capacity/pkg"
	"github.com/spf13/cobra"
)

var pod string

var recommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Recommend resource allocations",
	Run: func(cmd *cobra.Command, args []string) {
		client, err := pkg.NewPrometheusClient(promURL, logger)
		if err != nil {
			logger.WithError(err).Fatal("Failed to create Prometheus client")
		}

		if namespace == "" {
			logger.Fatal("Namespace is required")
		}

		// For simplicity, we are recommending CPU usage
		result, err := client.QueryCPUUsageForNamespace(namespace)
		if err != nil {
			logger.WithError(err).Fatal("Error fetching CPU usage")
		}

		fmt.Printf("Recommended CPU usage for namespace '%s': %v\n", namespace, result)
	},
}

func init() {
	rootCmd.AddCommand(recommendCmd)
	recommendCmd.Flags().StringVarP(&pod, "pod", "p", "", "Pod to recommend resources for")
}
