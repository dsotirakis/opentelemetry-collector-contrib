// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package splunkenterprisereceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/splunkenterprisereceiver"

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/scrapererror"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/splunkenterprisereceiver/internal/metadata"
)

var (
	errMaxSearchWaitTimeExceeded = errors.New("maximum search wait time exceeded for metric")
)

type splunkScraper struct {
	splunkClient *splunkEntClient
	settings     component.TelemetrySettings
	conf         *Config
	mb           *metadata.MetricsBuilder
}

func newSplunkMetricsScraper(params receiver.CreateSettings, cfg *Config) splunkScraper {
	return splunkScraper{
		settings: params.TelemetrySettings,
		conf:     cfg,
		mb:       metadata.NewMetricsBuilder(cfg.MetricsBuilderConfig, params),
	}
}

// Create a client instance and add to the splunkScraper
func (s *splunkScraper) start(_ context.Context, h component.Host) (err error) {
	client, err := newSplunkEntClient(s.conf, h, s.settings)
	if err != nil {
		return err
	}
	s.splunkClient = client
	return nil
}

// The big one: Describes how all scraping tasks should be performed. Part of the scraper interface
func (s *splunkScraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	errs := &scrapererror.ScrapeErrors{}
	now := pcommon.NewTimestampFromTime(time.Now())

	s.scrapeLicenseUsageByIndex(ctx, now, errs)
	s.scrapeAvgExecLatencyByHost(ctx, now, errs)
	s.scrapeSchedulerCompletionRatioByHost(ctx, now, errs)
	s.scrapeIndexerAvgRate(ctx, now, errs)
	s.scrapeSchedulerRunTimeByHost(ctx, now, errs)
	s.scrapeIndexerRawWriteSecondsByHost(ctx, now, errs)
	s.scrapeIndexerCPUSecondsByHost(ctx, now, errs)
	s.scrapeAvgIopsByHost(ctx, now, errs)
	s.scrapeIndexThroughput(ctx, now, errs)
	s.scrapeIndexesTotalSize(ctx, now, errs)
	s.scrapeIndexesEventCount(ctx, now, errs)
	s.scrapeIndexesBucketCount(ctx, now, errs)
	s.scrapeIndexesRawSize(ctx, now, errs)
	s.scrapeIndexesBucketEventCount(ctx, now, errs)
	s.scrapeIndexesBucketHotWarmCount(ctx, now, errs)
	s.scrapeIntrospectionQueues(ctx, now, errs)
	s.scrapeIntrospectionQueuesBytes(ctx, now, errs)
	s.scrapeIndexerPipelineQueues(ctx, now, errs)
	s.scrapeBucketsSearchableStatus(ctx, now, errs)
	s.scrapeIndexesBucketCountAdHoc(ctx, now, errs)
	return s.mb.Emit(), errs.Combine()
}

// Each metric has its own scrape function associated with it
func (s *splunkScraper) scrapeLicenseUsageByIndex(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkLicenseIndexUsage.Enabled || !s.splunkClient.isConfigured(typeCm) {
		return
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	sr := searchResponse{
		search: searchDict[`SplunkLicenseIndexUsageSearch`],
	}

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var indexName string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "indexname":
			indexName = f.Value
			continue
		case "By":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkLicenseIndexUsageDataPoint(now, int64(v), indexName)
		}
	}
}

func (s *splunkScraper) scrapeAvgExecLatencyByHost(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkSchedulerAvgExecutionLatency.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkSchedulerAvgExecLatencySearch`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "latency_avg_exec":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkSchedulerAvgExecutionLatencyDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeIndexerAvgRate(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkIndexerAvgRate.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkIndexerAvgRate`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 200 {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}
	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "indexer_avg_kbps":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexerAvgRateDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeIndexerPipelineQueues(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkAggregationQueueRatio.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkPipelineQueues`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}

		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 200 {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}

	}
	// Record the results
	var host string
	var ps int64
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "agg_queue_ratio":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkAggregationQueueRatioDataPoint(now, v, host)
		case "index_queue_ratio":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexerQueueRatioDataPoint(now, v, host)
		case "parse_queue_ratio":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkParseQueueRatioDataPoint(now, v, host)
		case "pipeline_sets":
			v, err := strconv.ParseInt(f.Value, 10, 64)
			ps = v
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkPipelineSetCountDataPoint(now, ps, host)
		case "typing_queue_ratio":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkTypingQueueRatioDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeBucketsSearchableStatus(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkBucketsSearchableStatus.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkBucketsSearchableStatus`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}

		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 200 {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}
	// Record the results
	var host string
	var searchable string
	var bc int64
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "is_searchable":
			searchable = f.Value
			continue
		case "bucket_count":
			v, err := strconv.ParseInt(f.Value, 10, 64)
			bc = v
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkBucketsSearchableStatusDataPoint(now, bc, host, searchable)
		}
	}
}

func (s *splunkScraper) scrapeIndexesBucketCountAdHoc(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkIndexesSize.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkIndexesData`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results

		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 200 {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}
	// Record the results
	var indexer string
	var bc int64
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "title":
			indexer = f.Value
			continue
		case "total_size_gb":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexesSizeDataPoint(now, v, indexer)
		case "average_size_gb":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexesAvgSizeDataPoint(now, v, indexer)
		case "average_usage_perc":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexesAvgUsageDataPoint(now, v, indexer)
		case "median_data_age":
			v, err := strconv.ParseInt(f.Value, 10, 64)
			bc = v
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexesMedianDataAgeDataPoint(now, bc, indexer)
		case "bucket_count":
			v, err := strconv.ParseInt(f.Value, 10, 64)
			bc = v
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexesBucketCountDataPoint(now, bc, indexer)
		}
	}
}

func (s *splunkScraper) scrapeSchedulerCompletionRatioByHost(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkSchedulerCompletionRatio.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkSchedulerCompletionRatio`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "completion_ratio":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkSchedulerCompletionRatioDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeIndexerRawWriteSecondsByHost(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkIndexerRawWriteTime.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkIndexerRawWriteSeconds`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "raw_data_write_seconds":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexerRawWriteTimeDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeIndexerCPUSecondsByHost(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkIndexerCPUTime.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkIndexerCpuSeconds`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "service_cpu_seconds":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIndexerCPUTimeDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeAvgIopsByHost(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkIoAvgIops.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkIoAvgIops`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "iops":
			v, err := strconv.ParseInt(f.Value, 10, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkIoAvgIopsDataPoint(now, v, host)
		}
	}
}

func (s *splunkScraper) scrapeSchedulerRunTimeByHost(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	// Because we have to utilize network resources for each KPI we should check that each metrics
	// is enabled before proceeding
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkSchedulerAvgRunTime.Enabled {
		return
	}

	sr := searchResponse{
		search: searchDict[`SplunkSchedulerAvgRunTime`],
	}
	ctx = context.WithValue(ctx, endpointType("type"), typeCm)

	var (
		req *http.Request
		res *http.Response
		err error
	)

	start := time.Now()

	for {
		req, err = s.splunkClient.createRequest(ctx, &sr)
		if err != nil {
			errs.Add(err)
			return
		}

		res, err = s.splunkClient.makeRequest(req)
		if err != nil {
			errs.Add(err)
			return
		}

		// if its a 204 the body will be empty because we are still waiting on search results
		err = unmarshallSearchReq(res, &sr)
		if err != nil {
			errs.Add(err)
		}
		res.Body.Close()

		// if no errors and 200 returned scrape was successful, return. Note we must make sure that
		// the 200 is coming after the first request which provides a jobId to retrieve results
		if sr.Return == 200 && sr.Jobid != nil {
			break
		}

		if sr.Return == 204 {
			time.Sleep(2 * time.Second)
		}

		if sr.Return == 400 {
			break
		}

		if time.Since(start) > s.conf.ScraperControllerSettings.Timeout {
			errs.Add(errMaxSearchWaitTimeExceeded)
			return
		}
	}

	// Record the results
	var host string
	for _, f := range sr.Fields {
		switch fieldName := f.FieldName; fieldName {
		case "host":
			host = f.Value
			continue
		case "run_time_avg":
			v, err := strconv.ParseFloat(f.Value, 64)
			if err != nil {
				errs.Add(err)
				continue
			}
			s.mb.RecordSplunkSchedulerAvgRunTimeDataPoint(now, v, host)
		}
	}
}

// Helper function for unmarshaling search endpoint requests
func unmarshallSearchReq(res *http.Response, sr *searchResponse) error {
	sr.Return = res.StatusCode

	if res.ContentLength == 0 {
		return nil
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("Failed to read response: %w", err)
	}

	err = xml.Unmarshal(body, &sr)
	if err != nil {
		return fmt.Errorf("Failed to unmarshall response: %w", err)
	}

	return nil
}

// Scrape index throughput introspection endpoint
func (s *splunkScraper) scrapeIndexThroughput(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkIndexerThroughput.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it indexThroughput

	ept := apiDict[`SplunkIndexerThroughput`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	for _, entry := range it.Entries {
		s.mb.RecordSplunkIndexerThroughputDataPoint(now, 1000*entry.Content.AvgKb, entry.Content.Status)
	}
}

// Scrape indexes extended total size
func (s *splunkScraper) scrapeIndexesTotalSize(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkDataIndexesExtendedTotalSize.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IndexesExtended
	ept := apiDict[`SplunkDataIndexesExtended`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	var totalSize int64
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}
		if f.Content.TotalSize != "" {
			mb, err := strconv.ParseFloat(f.Content.TotalSize, 64)
			totalSize = int64(mb * 1024 * 1024)
			if err != nil {
				errs.Add(err)
			}
		}

		s.mb.RecordSplunkDataIndexesExtendedTotalSizeDataPoint(now, totalSize, name)
	}
}

// Scrape indexes extended total event count
func (s *splunkScraper) scrapeIndexesEventCount(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkDataIndexesExtendedEventCount.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IndexesExtended

	ept := apiDict[`SplunkDataIndexesExtended`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}
		totalEventCount := int64(f.Content.TotalEventCount)

		s.mb.RecordSplunkDataIndexesExtendedEventCountDataPoint(now, totalEventCount, name)
	}
}

// Scrape indexes extended total bucket count
func (s *splunkScraper) scrapeIndexesBucketCount(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkDataIndexesExtendedBucketCount.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IndexesExtended

	ept := apiDict[`SplunkDataIndexesExtended`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	var totalBucketCount int64
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}
		if f.Content.TotalBucketCount != "" {
			totalBucketCount, err = strconv.ParseInt(f.Content.TotalBucketCount, 10, 64)
			if err != nil {
				errs.Add(err)
			}
		}

		s.mb.RecordSplunkDataIndexesExtendedBucketCountDataPoint(now, totalBucketCount, name)
	}
}

// Scrape indexes extended raw size
func (s *splunkScraper) scrapeIndexesRawSize(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkDataIndexesExtendedRawSize.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IndexesExtended

	ept := apiDict[`SplunkDataIndexesExtended`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	var totalRawSize int64
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}
		if f.Content.TotalRawSize != "" {
			mb, err := strconv.ParseFloat(f.Content.TotalRawSize, 64)
			totalRawSize = int64(mb * 1024 * 1024)
			if err != nil {
				errs.Add(err)
			}
		}
		s.mb.RecordSplunkDataIndexesExtendedRawSizeDataPoint(now, totalRawSize, name)
	}
}

// Scrape indexes extended bucket event count
func (s *splunkScraper) scrapeIndexesBucketEventCount(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkDataIndexesExtendedBucketEventCount.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IndexesExtended

	ept := apiDict[`SplunkDataIndexesExtended`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	var bucketDir string
	var bucketEventCount int64
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}
		if f.Content.BucketDirs.Cold.EventCount != "" {
			bucketDir = "cold"
			bucketEventCount, err = strconv.ParseInt(f.Content.BucketDirs.Cold.EventCount, 10, 64)
			if err != nil {
				errs.Add(err)
			}
			s.mb.RecordSplunkDataIndexesExtendedBucketEventCountDataPoint(now, bucketEventCount, name, bucketDir)
		}
		if f.Content.BucketDirs.Home.EventCount != "" {
			bucketDir = "home"
			bucketEventCount, err = strconv.ParseInt(f.Content.BucketDirs.Home.EventCount, 10, 64)
			if err != nil {
				errs.Add(err)
			}
			s.mb.RecordSplunkDataIndexesExtendedBucketEventCountDataPoint(now, bucketEventCount, name, bucketDir)
		}
		if f.Content.BucketDirs.Thawed.EventCount != "" {
			bucketDir = "thawed"
			bucketEventCount, err = strconv.ParseInt(f.Content.BucketDirs.Thawed.EventCount, 10, 64)
			if err != nil {
				errs.Add(err)
			}
			s.mb.RecordSplunkDataIndexesExtendedBucketEventCountDataPoint(now, bucketEventCount, name, bucketDir)
		}
	}
}

// Scrape indexes extended bucket hot/warm count
func (s *splunkScraper) scrapeIndexesBucketHotWarmCount(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkDataIndexesExtendedBucketHotCount.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IndexesExtended

	ept := apiDict[`SplunkDataIndexesExtended`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	var bucketDir string
	var bucketHotCount int64
	var bucketWarmCount int64
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}
		if f.Content.BucketDirs.Home.HotBucketCount != "" {
			bucketHotCount, err = strconv.ParseInt(f.Content.BucketDirs.Home.HotBucketCount, 10, 64)
			bucketDir = "hot"
			if err != nil {
				errs.Add(err)
			}
			s.mb.RecordSplunkDataIndexesExtendedBucketHotCountDataPoint(now, bucketHotCount, name, bucketDir)
		}
		if f.Content.BucketDirs.Home.WarmBucketCount != "" {
			bucketWarmCount, err = strconv.ParseInt(f.Content.BucketDirs.Home.WarmBucketCount, 10, 64)
			bucketDir = "warm"
			if err != nil {
				errs.Add(err)
			}
			s.mb.RecordSplunkDataIndexesExtendedBucketWarmCountDataPoint(now, bucketWarmCount, name, bucketDir)
		}
	}
}

// Scrape introspection queues
func (s *splunkScraper) scrapeIntrospectionQueues(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkServerIntrospectionQueuesCurrent.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IntrospectionQueues

	ept := apiDict[`SplunkIntrospectionQueues`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}

	var name string
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}

		currentQueuesSize := int64(f.Content.CurrentSize)

		s.mb.RecordSplunkServerIntrospectionQueuesCurrentDataPoint(now, currentQueuesSize, name)
	}
}

// Scrape introspection queues bytes
func (s *splunkScraper) scrapeIntrospectionQueuesBytes(ctx context.Context, now pcommon.Timestamp, errs *scrapererror.ScrapeErrors) {
	if !s.conf.MetricsBuilderConfig.Metrics.SplunkServerIntrospectionQueuesCurrentBytes.Enabled || !s.splunkClient.isConfigured(typeIdx) {
		return
	}

	ctx = context.WithValue(ctx, endpointType("type"), typeIdx)
	var it IntrospectionQueues

	ept := apiDict[`SplunkIntrospectionQueues`]

	req, err := s.splunkClient.createAPIRequest(ctx, ept)
	if err != nil {
		errs.Add(err)
		return
	}

	res, err := s.splunkClient.makeRequest(req)
	if err != nil {
		errs.Add(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		errs.Add(err)
		return
	}

	err = json.Unmarshal(body, &it)
	if err != nil {
		errs.Add(err)
		return
	}
	var name string
	for _, f := range it.Entries {
		if f.Name != "" {
			name = f.Name
		}

		currentQueueSizeBytes := int64(f.Content.CurrentSizeBytes)

		s.mb.RecordSplunkServerIntrospectionQueuesCurrentBytesDataPoint(now, currentQueueSizeBytes, name)
	}
}
