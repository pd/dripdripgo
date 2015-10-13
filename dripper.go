package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"time"
)

type Dripper struct {
	N         int
	heapSize  uint64
	appName   string
	authToken string
	client    *http.Client
	logger    *log.Logger
	metrics   map[string]string
}

func NewDripper(n int, heapSize uint64) *Dripper {
	name := fmt.Sprintf("guava%d", n)
	return &Dripper{
		N:        n,
		heapSize: heapSize,
		appName:  name,
		client:   &http.Client{Timeout: time.Minute},
		logger:   log.New(os.Stderr, name+" ", log.Ldate|log.Ltime),
		metrics:  make(map[string]string),
	}
}

func (d *Dripper) String() string {
	return fmt.Sprintf("Dripper[%s]", d.appName)
}

func (d *Dripper) Drip() {
	token, err := d.PostInit()
	if err != nil {
		d.logger.Printf("Init() failed: %s", err)
		return
	}

	d.authToken = token
	d.logger.Printf("AuthToken: %s", token)

	if err := d.PostMetrics(); err != nil {
		d.logger.Printf("Initial PostMetrics() failed: %s", err)
		return
	}

	if err := d.PostModules(); err != nil {
		d.logger.Printf("PostModules() failed: %s", err)
		return
	}

	ticker := time.NewTicker(time.Minute)
	for {
		select {
		case <-ticker.C:
			if err := d.PostMetrics(); err != nil {
				log.Printf("PostMetrics() failed: %s", err)
			}
		}
	}
}

func (d *Dripper) PostInit() (string, error) {
	body := bytes.NewBufferString(fmt.Sprintf(
		initBody,
		12345+d.N,     // pid
		d.heapSize/10, // heapInitialBytes
		d.heapSize,    // heapMaxBytes
	))
	req, err := d.newPost("/init", body)
	if err != nil {
		return "", err
	}

	response, err := d.do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	var initResp struct {
		AuthToken string `json:"authtoken"`
	}

	if err := json.NewDecoder(response.Body).Decode(&initResp); err != nil {
		return "", err
	}

	return initResp.AuthToken, nil
}

type metricsSubmission struct {
	Metrics    []map[string]interface{} `json:"metrics"`
	AppMetrics []map[string]interface{} `json:"appMetrics"`
}

func (d *Dripper) PostMetrics() error {
	metrics, err := d.newMetrics()
	if err != nil {
		return err
	}

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(&metrics); err != nil {
		return err
	}

	req, err := d.newPost("/v7/data", buf)
	if err != nil {
		return err
	}

	response, err := d.do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Expected 200, got %d\n", response.StatusCode)
	}

	var metricsResp struct {
		Metrics []map[string]interface{} `json:"metrics"`
	}
	if err := json.NewDecoder(response.Body).Decode(&metricsResp); err != nil {
		return err
	}

	if metricsResp.Metrics == nil || len(metricsResp.Metrics) == 0 {
		return nil
	}

	for _, entry := range metricsResp.Metrics {
		name := entry["name"].(string)
		id := entry["id"].(float64)
		d.metrics[name] = strconv.FormatUint(uint64(id), 10)
	}

	return nil
}

func (d *Dripper) PostModules() error {
	req, err := d.newPost("/moduleUpdate", bytes.NewBufferString(modulesBody))
	if err != nil {
		return err
	}

	response, err := d.do(req)
	if err != nil {
		return err
	}

	response.Body.Close()
	return nil
}

func (d *Dripper) newMetrics() (*metricsSubmission, error) {
	// too lazy to bother with building these reasonably.
	// just cycle through a marshalling.
	var metricsValue []map[string]interface{}
	var metricDataValue interface{}

	if err := json.Unmarshal([]byte(metricDataBody), &metricDataValue); err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(baseMetricsBody), &metricsValue); err != nil {
		return nil, err
	}

	submission := &metricsSubmission{
		Metrics: make([]map[string]interface{}, 0, len(metricsValue)),
		AppMetrics: []map[string]interface{}{
			{"appName": d.appName, "metricData": metricDataValue},
		},
	}

	for _, obj := range metricsValue {
		// there's actually only one key. this API is ... ugh.
		for k, v := range obj {
			var key string

			key = k
			entry := make(map[string]interface{}, 1)

			if d.metrics != nil {
				if id, ok := d.metrics[key]; ok {
					key = id
				}
			}

			entry[key] = d.valueForMetric(k, v)
			submission.Metrics = append(submission.Metrics, entry)
		}
	}

	return submission, nil
}

func (d *Dripper) valueForMetric(name string, value interface{}) interface{} {
	switch name {
	case "JVM/Memory/HeapCommitted", "JVM/Memory/HeapMax":
		return d.heapSize
	case "JVM/Hardware/RAM":
		return d.heapSize * 10
	default:
		return value
	}
}

func (d *Dripper) newPost(path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest("POST", fmt.Sprintf("https://datacollector.dripstat.com/agent/v1%s", path), body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "dripdripgo")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	if d.authToken != "" {
		req.Header.Set("X-Auth-Token", d.authToken)
	} else {
		req.Header.Set("X-License-Key", licenseKey)
		req.Header.Set("X-App-Name", d.appName)
	}

	return req, nil
}

func (d *Dripper) do(req *http.Request) (*http.Response, error) {
	if debugMode {
		dump, err := httputil.DumpRequestOut(req, true)
		if err != nil {
			return nil, err
		}
		d.logger.Println(string(dump))
	}

	response, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}

	if debugMode {
		dump, err := httputil.DumpResponse(response, true)
		if err != nil {
			return response, err
		}
		d.logger.Println(string(dump))
	}

	return response, err
}
