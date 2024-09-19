package pkg

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
)

type PrometheusClient struct {
	Client v1.API
	Logger *logrus.Logger
}

func NewPrometheusClient(prometheusURL string, logger *logrus.Logger) (*PrometheusClient, error) {
	client, err := api.NewClient(api.Config{
		Address: prometheusURL,
	})
	if err != nil {
		return nil, err
	}

	return &PrometheusClient{
		Client: v1.NewAPI(client),
		Logger: logger,
	}, nil
}

func (p *PrometheusClient) Query(query string) (model.Value, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, warnings, err := p.Client.Query(ctx, query, time.Now())
	if err != nil {
		p.Logger.WithError(err).Error("Error querying Prometheus")
		return nil, err
	}

	if len(warnings) > 0 {
		p.Logger.Warnf("Warnings: %v", warnings)
	}

	return result, nil
}

func (p *PrometheusClient) QueryCPUUsageForNamespace(namespace string) (model.Value, error) {
	query := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="%s"}[5m]))`, namespace)
	return p.Query(query)
}
