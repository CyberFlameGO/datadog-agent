package report

import (
	"fmt"
	"github.com/DataDog/datadog-agent/pkg/aggregator"
	"github.com/DataDog/datadog-agent/pkg/metrics"
	"github.com/DataDog/datadog-agent/pkg/util/log"

	"github.com/DataDog/datadog-agent/pkg/collector/corechecks/snmp/checkconfig"
	"github.com/DataDog/datadog-agent/pkg/collector/corechecks/snmp/common"
	"github.com/DataDog/datadog-agent/pkg/collector/corechecks/snmp/valuestore"
)

// MetricSender is a wrapper around aggregator.Sender
type MetricSender struct {
	sender           aggregator.Sender
	hostname         string
	submittedMetrics int
}

// NewMetricSender create a new MetricSender
func NewMetricSender(sender aggregator.Sender, hostname string) *MetricSender {
	return &MetricSender{sender: sender, hostname: hostname}
}

// ReportMetrics reports metrics using Sender
func (ms *MetricSender) ReportMetrics(metrics []checkconfig.MetricsConfig, values *valuestore.ResultValueStore, tags []string) {
	for _, metric := range metrics {
		if metric.IsScalar() {
			ms.reportScalarMetrics(metric, values, tags)
		} else if metric.IsColumn() {
			ms.reportColumnMetrics(metric, values, tags)
		}
	}
}

// GetCheckInstanceMetricTags returns check instance metric tags
func (ms *MetricSender) GetCheckInstanceMetricTags(metricTags []checkconfig.MetricTagConfig, values *valuestore.ResultValueStore) []string {
	var globalTags []string

	for _, metricTag := range metricTags {
		// TODO: Support extract value see II-635
		value, err := values.GetScalarValue(metricTag.OID)
		if err != nil {
			continue
		}
		strValue, err := value.ToString()
		if err != nil {
			log.Debugf("error converting value (%#v) to string : %v", value, err)
			continue
		}
		globalTags = append(globalTags, metricTag.GetTags(strValue)...)
	}
	return globalTags
}

func (ms *MetricSender) reportScalarMetrics(metric checkconfig.MetricsConfig, values *valuestore.ResultValueStore, tags []string) {
	value, err := getScalarValueFromSymbol(values, metric.Symbol)
	if err != nil {
		log.Debugf("report scalar: error getting scalar value: %v", err)
		return
	}

	scalarTags := common.CopyStrings(tags)
	scalarTags = append(scalarTags, metric.GetSymbolTags()...)
	ms.sendMetric(metric.Symbol.Name, value, scalarTags, metric.ForcedType, metric.Options)
}

func (ms *MetricSender) reportColumnMetrics(metricConfig checkconfig.MetricsConfig, values *valuestore.ResultValueStore, tags []string) {
	rowTagsCache := make(map[string][]string)
	for _, symbol := range metricConfig.Symbols {
		metricValues, err := getColumnValueFromSymbol(values, symbol)
		if err != nil {
			continue
		}
		for fullIndex, value := range metricValues {
			// cache row tags by fullIndex to avoid rebuilding it for every column rows
			if _, ok := rowTagsCache[fullIndex]; !ok {
				rowTagsCache[fullIndex] = append(common.CopyStrings(tags), metricConfig.MetricTags.GetTags(fullIndex, values)...)
			}
			rowTags := rowTagsCache[fullIndex]
			ms.sendMetric(symbol.Name, value, rowTags, metricConfig.ForcedType, metricConfig.Options)
			ms.trySendBandwidthUsageMetric(symbol, fullIndex, values, rowTags)
		}
	}
}

func (ms *MetricSender) sendMetric(metricName string, value valuestore.ResultValue, tags []string, forcedType string, options checkconfig.MetricsConfigOption) {
	metricFullName := "snmp." + metricName
	if forcedType == "" {
		if value.SubmissionType != "" {
			forcedType = value.SubmissionType
		} else {
			forcedType = "gauge"
		}
	} else if forcedType == "flag_stream" {
		strValue, err := value.ToString()
		if err != nil {
			log.Debugf("error converting value (%#v) to string : %v", value, err)
			return
		}
		floatValue, err := getFlagStreamValue(options.Placement, strValue)
		if err != nil {
			log.Debugf("metric `%s`: failed to get flag stream value: %s", metricFullName, err)
			return
		}
		metricFullName = metricFullName + "." + options.MetricSuffix
		value = valuestore.ResultValue{Value: floatValue}
		forcedType = "gauge"
	}

	floatValue, err := value.ToFloat64()
	if err != nil {
		log.Debugf("metric `%s`: failed to convert to float64: %s", metricFullName, err)
		return
	}

	switch forcedType {
	case "gauge":
		ms.Gauge(metricFullName, floatValue, tags)
		ms.submittedMetrics++
	case "counter":
		ms.Rate(metricFullName, floatValue, tags)
		ms.submittedMetrics++
	case "percent":
		ms.Rate(metricFullName, floatValue*100, tags)
		ms.submittedMetrics++
	case "monotonic_count":
		ms.MonotonicCount(metricFullName, floatValue, tags)
		ms.submittedMetrics++
	case "monotonic_count_and_rate":
		ms.MonotonicCount(metricFullName, floatValue, tags)
		ms.Rate(metricFullName+".rate", floatValue, tags)
		ms.submittedMetrics += 2
	default:
		log.Debugf("metric `%s`: unsupported forcedType: %s", metricFullName, forcedType)
		return
	}
}

// Gauge wraps Sender.Gauge
func (ms *MetricSender) Gauge(metric string, value float64, tags []string) {
	// we need copy tags before using Sender due to https://github.com/DataDog/datadog-agent/issues/7159
	ms.sender.Gauge(metric, value, ms.hostname, common.CopyStrings(tags))
}

// Rate wraps Sender.Rate
func (ms *MetricSender) Rate(metric string, value float64, tags []string) {
	// we need copy tags before using Sender due to https://github.com/DataDog/datadog-agent/issues/7159
	ms.sender.Rate(metric, value, ms.hostname, common.CopyStrings(tags))
}

// MonotonicCount wraps Sender.MonotonicCount
func (ms *MetricSender) MonotonicCount(metric string, value float64, tags []string) {
	// we need copy tags before using Sender due to https://github.com/DataDog/datadog-agent/issues/7159
	ms.sender.MonotonicCount(metric, value, ms.hostname, common.CopyStrings(tags))
}

// ServiceCheck wraps Sender.ServiceCheck
func (ms *MetricSender) ServiceCheck(checkName string, status metrics.ServiceCheckStatus, tags []string, message string) {
	// we need copy tags before using Sender due to https://github.com/DataDog/datadog-agent/issues/7159
	ms.sender.ServiceCheck(checkName, status, ms.hostname, common.CopyStrings(tags), message)
}

// GetSubmittedMetrics returns submitted metrics count
func (ms *MetricSender) GetSubmittedMetrics() int {
	return ms.submittedMetrics
}

func getFlagStreamValue(placement uint, strValue string) (float64, error) {
	index := placement - 1
	if int(index) >= len(strValue) {
		return 0, fmt.Errorf("flag stream index `%d` not found in `%s`", index, strValue)
	}
	charAtIndex := strValue[index]
	floatValue := 0.0
	if charAtIndex == '1' {
		floatValue = 1.0
	}
	return floatValue, nil
}
