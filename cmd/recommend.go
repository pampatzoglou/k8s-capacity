package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Declare global variables
var (
	debug                bool
	timeWindow           string
	prometheusURL        = getPrometheusURL() // Retrieve Prometheus URL dynamically
	cpuPercentile        float64              // Configurable CPU percentile
	memoryPercentile     float64              // Configurable Memory percentile
	recommendQuotas      bool                 // Flag to indicate if resource quota recommendations are requested
	recommendLimitRanges bool                 // Flag to indicate if limit range recommendations are requested
)

// getPrometheusURL returns the Prometheus URL from environment variable or defaults to localhost:9090
func getPrometheusURL() string {
	if url, exists := os.LookupEnv("PROMETHEUS_URL"); exists {
		return url
	}
	return "http://localhost:9090" // Default URL
}

// recommendCmd represents the recommend command
var recommendCmd = &cobra.Command{
	Use:   "recommend",
	Short: "Recommend resource limits and requests for each container and initContainer in Deployments and StatefulSets in a namespace",
	Run: func(cmd *cobra.Command, args []string) {
		namespace, _ := cmd.Flags().GetString("namespace")

		// Use the "default" namespace if none is provided
		if namespace == "" {
			fmt.Println("No namespace provided. Using the 'default' namespace.")
			namespace = "default"
		}

		// Load kubeconfig
		kubeconfig := clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			panic(err.Error())
		}

		// Create Kubernetes client
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			panic(err.Error())
		}

		// Get all Deployments in the namespace
		deployments, err := clientset.AppsV1().Deployments(namespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}

		// Iterate through the Deployments and print recommendations
		for _, deployment := range deployments.Items {
			fmt.Printf("Deployment: %s\n", deployment.Name)
			printContainerRecommendations("InitContainer", deployment.Spec.Template.Spec.InitContainers, namespace, clientset)
			printContainerRecommendations("Container", deployment.Spec.Template.Spec.Containers, namespace, clientset)
		}

		// Get all StatefulSets in the namespace
		statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}

		// Iterate through the StatefulSets and print recommendations
		for _, statefulSet := range statefulSets.Items {
			fmt.Printf("StatefulSet: %s\n", statefulSet.Name)
			printContainerRecommendations("InitContainer", statefulSet.Spec.Template.Spec.InitContainers, namespace, clientset)
			printContainerRecommendations("Container", statefulSet.Spec.Template.Spec.Containers, namespace, clientset)
		}

		// Recommend resource quotas and limit ranges if requested
		if recommendQuotas {
			recommendResourceQuotas(namespace)
		}
		if recommendLimitRanges {
			recommendLimitRangesFunc(namespace)
		}
	},
}

// queryPrometheus queries Prometheus for the container's CPU and memory usage percentile
func queryPrometheus(namespace, container string) (cpuAvg, cpuMax, memoryAvg, memoryMax float64) {
	if timeWindow == "" {
		timeWindow = "30m" // Default to 30 minutes
	}

	// Ensure both percentiles have values; if not, use the cpuPercentile for memory as well
	if memoryPercentile == 0 {
		memoryPercentile = cpuPercentile // Default memory to use the same percentile as CPU
	}

	// Construct Prometheus queries for CPU percentile
	cpuAvgQuery := fmt.Sprintf(
		`quantile_over_time(0.5, node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate{namespace="%s", container="%s"}[%s])`,
		namespace, container, timeWindow,
	)
	cpuMaxQuery := fmt.Sprintf(
		`quantile_over_time(%.2f, node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate{namespace="%s", container="%s"}[%s])`,
		cpuPercentile, namespace, container, timeWindow,
	)

	// Construct Prometheus queries for Memory percentile
	memoryAvgQuery := fmt.Sprintf(
		`quantile_over_time(0.5, container_memory_usage_bytes{namespace="%s", container="%s"}[%s]) / (1024 * 1024 * 1024)`, // Convert to GiB
		namespace, container, timeWindow,
	)
	memoryMaxQuery := fmt.Sprintf(
		`quantile_over_time(%.2f, container_memory_usage_bytes{namespace="%s", container="%s"}[%s]) / (1024 * 1024 * 1024)`, // Convert to GiB
		memoryPercentile, namespace, container, timeWindow,
	)

	// Query Prometheus
	cpuAvg = queryPrometheusMetric(cpuAvgQuery)
	cpuMax = queryPrometheusMetric(cpuMaxQuery)
	memoryAvg = queryPrometheusMetric(memoryAvgQuery)
	memoryMax = queryPrometheusMetric(memoryMaxQuery)

	return cpuAvg, cpuMax, memoryAvg, memoryMax
}

func queryPrometheusMetric(query string) float64 {
	// URL-encode the entire query
	encodedQuery := url.QueryEscape(query)

	// Construct the full URL for the Prometheus API
	fullURL := fmt.Sprintf("%s/api/v1/query?query=%s", prometheusURL, encodedQuery)

	if debug {
		// Log the full URL for debugging
		fmt.Printf("Full Prometheus query URL: %s\n", fullURL)
	}

	// Send the HTTP GET request to Prometheus
	resp, err := http.Get(fullURL)
	if err != nil {
		fmt.Printf("Error querying Prometheus: %v\n", err)
		return 0
	}
	defer resp.Body.Close()

	if debug {
		// Log the response status for debugging
		fmt.Printf("Prometheus response status: %s\n", resp.Status)
	}

	// Check if the response status is not 200 OK
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Received non-OK HTTP status: %s\n", resp.Status)
		return 0
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading Prometheus response body: %v\n", err)
		return 0
	}

	if debug {
		// Log the raw response body for debugging
		fmt.Printf("Raw Prometheus response: %s\n", string(body))
	}

	// Unmarshal the JSON response
	var result struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Results    []struct {
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("Error parsing Prometheus response: %v\n", err)
		return 0
	}

	if result.Status != "success" || len(result.Data.Results) == 0 {
		return 0
	}

	// Extract the value from the first result
	value := result.Data.Results[0].Value[1]
	var floatValue float64
	switch v := value.(type) {
	case string:
		if _, err := fmt.Sscanf(v, "%f", &floatValue); err != nil {
			fmt.Printf("Error converting Prometheus value to float: %v\n", err)
			return 0
		}
	case float64:
		floatValue = v
	default:
		fmt.Printf("Unexpected value type: %T\n", v)
		return 0
	}

	return floatValue
}

// printContainerRecommendations prints resource recommendations for a slice of containers or initContainers
func printContainerRecommendations(containerType string, containers []corev1.Container, namespace string, clientset *kubernetes.Clientset) {
	for _, container := range containers {
		fmt.Printf("  %s: %s\n", containerType, container.Name)

		// Query Prometheus for the container's resource usage
		cpuAvg, cpuMax, memoryAvg, memoryMax := queryPrometheus(namespace, container.Name)

		// Print current resource requests and limits
		requests := container.Resources.Requests
		limits := container.Resources.Limits
		fmt.Printf("    Requests: CPU=%s, Memory=%s\n", requests.Cpu().String(), requests.Memory().String())
		fmt.Printf("    Limits:   CPU=%s, Memory=%s\n", limits.Cpu().String(), limits.Memory().String())

		// Format the Prometheus metrics into Kubernetes manifest compatible units
		recommendedCPURequest := formatCPU(cpuAvg)          // Convert from cores to millicores or cores
		recommendedMemoryRequest := formatMemory(memoryAvg) // Convert from GiB to MiB
		recommendedCPULimit := formatCPU(cpuMax)            // Convert from cores to millicores or cores
		recommendedMemoryLimit := formatMemory(memoryMax)   // Convert from GiB to MiB

		// Print recommended resources in Kubernetes manifest format
		fmt.Println("    Recommended resources:")
		fmt.Println("        limits:")
		fmt.Printf("          cpu: %s\n", recommendedCPULimit)
		fmt.Printf("          memory: %s\n", recommendedMemoryLimit)
		fmt.Println("        requests:")
		fmt.Printf("          cpu: %s\n", recommendedCPURequest)
		fmt.Printf("          memory: %s\n", recommendedMemoryRequest)
	}
}

// formatCPU formats CPU usage to Kubernetes-compatible units
func formatCPU(cpu float64) string {
	if cpu < 0.001 {
		return "1m" // Handle very small values
	} else if cpu < 0.1 {
		return fmt.Sprintf("%.0fm", cpu*1000) // Convert to millicores
	} else if cpu < 1 {
		return fmt.Sprintf("%.0fm", cpu*1000) // Convert to millicores
	}
	return fmt.Sprintf("%.0f", cpu) // Convert to cores
}

// formatMemory formats memory usage to Kubernetes-compatible units
func formatMemory(memory float64) string {
	if memory >= 1024 {
		return fmt.Sprintf("%.0fGi", memory/1024) // Convert from MiB to GiB
	}
	return fmt.Sprintf("%.0fMi", memory) // Use MiB for smaller values
}

// recommendResourceQuotas recommends resource quotas for the namespace
func recommendResourceQuotas(namespace string) {
	// Example logic for recommending resource quotas
	fmt.Println("Recommended Resource Quotas:")
	fmt.Println("  hard:")
	fmt.Println("    cpu: 4")
	fmt.Println("    memory: 8Gi")
	fmt.Println("    pods: 10")
	fmt.Println("    configmaps: 10")
	fmt.Println("    secrets: 10")
}

// recommendLimitRangesFunc recommends limit ranges for the namespace
func recommendLimitRangesFunc(namespace string) {
	// Example suggested values (could be based on data from Prometheus or other sources)
	suggestedMinCPU := "50m"
	suggestedMinMemory := "50Mi"
	suggestedMaxCPU := "2"
	suggestedMaxMemory := "2Gi"
	suggestedDefaultCPU := "500m"
	suggestedDefaultMemory := "500Mi"
	suggestedDefaultRequestCPU := "100m"
	suggestedDefaultRequestMemory := "100Mi"

	// Convert suggested memory values to MiB for comparison
	minMemoryMiB := convertMemoryToMiB(suggestedMinMemory)
	maxMemoryMiB := convertMemoryToMiB(suggestedMaxMemory)
	defaultMemoryMiB := convertMemoryToMiB(suggestedDefaultMemory)
	defaultRequestMemoryMiB := convertMemoryToMiB(suggestedDefaultRequestMemory)

	// Compute the maximum memory value from the suggested values
	maxMemory := max(minMemoryMiB, maxMemoryMiB, defaultMemoryMiB, defaultRequestMemoryMiB)

	// Print the recommended limit ranges
	fmt.Println("Recommended Limit Ranges:")
	fmt.Println("  limits:")
	fmt.Println("    min:")
	fmt.Printf("      cpu: %s\n", suggestedMinCPU)
	fmt.Printf("      memory: %s\n", suggestedMinMemory)
	fmt.Println("    max:")
	fmt.Printf("      cpu: %s\n", suggestedMaxCPU)
	fmt.Printf("      memory: %s\n", formatMemory(maxMemory)) // Format the max memory value
	fmt.Println("    default:")
	fmt.Printf("      cpu: %s\n", suggestedDefaultCPU)
	fmt.Printf("      memory: %s\n", suggestedDefaultMemory)
	fmt.Println("    defaultRequest:")
	fmt.Printf("      cpu: %s\n", suggestedDefaultRequestCPU)
	fmt.Printf("      memory: %s\n", suggestedDefaultRequestMemory)
}

// convertMemoryToMiB converts a memory value in string format to MiB
func convertMemoryToMiB(memory string) float64 {
	var value float64
	var unit string

	fmt.Sscanf(memory, "%f%s", &value, &unit)

	switch unit {
	case "Gi":
		return value * 1024
	case "Mi":
		return value
	default:
		// Assuming MiB if unit is unknown
		return value
	}
}

// max returns the maximum value from a list of values
func max(values ...float64) float64 {
	var maxValue float64
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func init() {
	rootCmd.AddCommand(recommendCmd)

	recommendCmd.Flags().StringP("namespace", "n", "", "The namespace to get Deployments and StatefulSets from (default is 'default')")
	recommendCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug output")
	recommendCmd.Flags().StringP("timewindow", "t", "1d", "Time window for Prometheus queries (default is '30m')")
	recommendCmd.Flags().Float64Var(&cpuPercentile, "cpu-percentile", 0.99, "Percentile to use for CPU resource limits (default is 99th percentile)")
	recommendCmd.Flags().Float64Var(&memoryPercentile, "memory-percentile", 0.99, "Percentile to use for memory resource limits (default is 99th percentile)")
	recommendCmd.Flags().BoolVar(&recommendQuotas, "recommend-quotas", false, "Recommend resource quotas for the namespace")
	recommendCmd.Flags().BoolVar(&recommendLimitRanges, "recommend-limit-ranges", false, "Recommend limit ranges for the namespace")
}
