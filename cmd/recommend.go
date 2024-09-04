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

var prometheusURL = getPrometheusURL() // Retrieve Prometheus URL dynamically
var debug bool                         // Flag for enabling debug output

// getPrometheusURL returns the Prometheus URL from environment variable or default to localhost:9090
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
			printContainerRecommendations("InitContainer", deployment.Spec.Template.Spec.InitContainers, namespace)
			printContainerRecommendations("Container", deployment.Spec.Template.Spec.Containers, namespace)
		}

		// Get all StatefulSets in the namespace
		statefulSets, err := clientset.AppsV1().StatefulSets(namespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}

		// Iterate through the StatefulSets and print recommendations
		for _, statefulSet := range statefulSets.Items {
			fmt.Printf("StatefulSet: %s\n", statefulSet.Name)
			printContainerRecommendations("InitContainer", statefulSet.Spec.Template.Spec.InitContainers, namespace)
			printContainerRecommendations("Container", statefulSet.Spec.Template.Spec.Containers, namespace)
		}
	},
}

// printContainerRecommendations prints resource recommendations for a slice of containers or initContainers
func printContainerRecommendations(containerType string, containers []corev1.Container, namespace string) {
	for _, container := range containers {
		fmt.Printf("  %s: %s\n", containerType, container.Name)

		// Query Prometheus for the container's resource usage
		cpuUsage, memoryUsage := queryPrometheus(namespace, container.Name)

		// Print current resource requests and limits
		requests := container.Resources.Requests
		limits := container.Resources.Limits
		fmt.Printf("    Requests: CPU=%s, Memory=%s\n", requests.Cpu().String(), requests.Memory().String())
		fmt.Printf("    Limits:   CPU=%s, Memory=%s\n", limits.Cpu().String(), limits.Memory().String())

		// Print Prometheus metrics
		fmt.Printf("    CPU Usage: %.2f\n", cpuUsage)
		fmt.Printf("    Memory Usage: %.2f GiB\n", memoryUsage)

		// Example recommendation logic (static in this case)
		recommendedCPURequest := "100m"
		recommendedMemoryRequest := "128Mi"
		recommendedCPULimit := "200m"
		recommendedMemoryLimit := "256Mi"

		fmt.Printf("    Recommended Requests: CPU=%s, Memory=%s\n", recommendedCPURequest, recommendedMemoryRequest)
		fmt.Printf("    Recommended Limits:   CPU=%s, Memory=%s\n", recommendedCPULimit, recommendedMemoryLimit)
	}
}

// queryPrometheus queries Prometheus for the container's CPU and memory usage
func queryPrometheus(namespace, container string) (float64, float64) {
	// Construct Prometheus queries
	cpuQuery := fmt.Sprintf(
		`sum(node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate{namespace="%s", container="%s"})`,
		namespace, container,
	)
	memoryQuery := fmt.Sprintf(
		`sum(container_memory_usage_bytes{namespace="%s", container="%s"}) / (1024 * 1024 * 1024)`,
		namespace, container,
	)

	// URL-encode the queries
	encodedCPUQuery := url.QueryEscape(cpuQuery)
	encodedMemoryQuery := url.QueryEscape(memoryQuery)

	// Query Prometheus for CPU usage
	cpuUsage := queryPrometheusMetric(encodedCPUQuery)

	// Query Prometheus for memory usage
	memoryUsage := queryPrometheusMetric(encodedMemoryQuery)

	return cpuUsage, memoryUsage
}

// queryPrometheusMetric sends a query to Prometheus and parses the result
func queryPrometheusMetric(query string) float64 {
	url := fmt.Sprintf("%s/api/v1/query?query=%s", prometheusURL, query)

	if debug {
		// Log the request URL
		fmt.Printf("Querying Prometheus: %s\n", url)
	}

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error querying Prometheus: %v\n", err)
		return 0
	}
	defer resp.Body.Close()

	if debug {
		// Log the response status
		fmt.Printf("Prometheus response status: %s\n", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading Prometheus response body: %v\n", err)
		return 0
	}

	if debug {
		// Log the response body
		fmt.Printf("Prometheus response body: %s\n", body)
	}

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

	// Assuming a single value result
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

func init() {
	rootCmd.AddCommand(recommendCmd)

	// Define flags and configuration settings.
	recommendCmd.Flags().StringP("namespace", "n", "", "The namespace to get Deployments and StatefulSets from (default is 'default')")
	recommendCmd.Flags().BoolVarP(&debug, "debug", "d", false, "Enable debug output")
}
