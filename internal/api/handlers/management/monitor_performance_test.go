package management

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestGetMonitorHourlyPerformance_MinuteGranularityFiltersZeroLatency(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Local().Truncate(time.Minute)
	slotTime := now.Add(-2 * time.Minute)
	otherSlotTime := now.Add(-1 * time.Minute)

	h := newMonitorTestHandler(
		testUsageRecord(slotTime.Add(5*time.Second), "api-1", "model-a", "source-a", false, 1200, 150),
		testUsageRecord(slotTime.Add(15*time.Second), "api-1", "model-a", "source-a", false, 1500, 0),
		testUsageRecord(slotTime.Add(25*time.Second), "api-1", "model-a", "source-a", true, 1800, 450),
		testUsageRecord(otherSlotTime.Add(10*time.Second), "api-1", "model-a", "source-a", false, 900, 300),
	)

	rr := executeMonitorRequest(h.GetMonitorHourlyPerformance, "/monitor/hourly-performance?hours=6&granularity=minute")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots                  []string  `json:"slots"`
		AvgRPM                 []float64 `json:"avg_rpm"`
		AvgFirstTokenLatencyMs []float64 `json:"avg_first_token_latency_ms"`
		Granularity            string    `json:"granularity"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Granularity != "minute" {
		t.Fatalf("unexpected granularity: %s", resp.Granularity)
	}
	if len(resp.Slots) != 360 {
		t.Fatalf("unexpected slot count: got %d want 360", len(resp.Slots))
	}
	if len(resp.AvgRPM) != len(resp.Slots) || len(resp.AvgFirstTokenLatencyMs) != len(resp.Slots) {
		t.Fatalf("unexpected series length: slots=%d rpm=%d latency=%d", len(resp.Slots), len(resp.AvgRPM), len(resp.AvgFirstTokenLatencyMs))
	}

	findSlot := func(target time.Time) int {
		expected := target.Format("2006-01-02T15:04:05-07:00")
		for i, slot := range resp.Slots {
			if slot == expected {
				return i
			}
		}
		return -1
	}

	slotIndex := findSlot(slotTime)
	if slotIndex < 0 {
		t.Fatalf("target slot not found: %s", slotTime.Format(time.RFC3339))
	}
	if math.Abs(resp.AvgRPM[slotIndex]-3) > 0.0001 {
		t.Fatalf("unexpected avg_rpm for slot: got %.4f want 3.0000", resp.AvgRPM[slotIndex])
	}
	if math.Abs(resp.AvgFirstTokenLatencyMs[slotIndex]-150) > 0.0001 {
		t.Fatalf("unexpected avg_first_token_latency_ms for slot: got %.4f want 150.0000", resp.AvgFirstTokenLatencyMs[slotIndex])
	}

	otherSlotIndex := findSlot(otherSlotTime)
	if otherSlotIndex < 0 {
		t.Fatalf("other slot not found: %s", otherSlotTime.Format(time.RFC3339))
	}
	if math.Abs(resp.AvgRPM[otherSlotIndex]-1) > 0.0001 {
		t.Fatalf("unexpected avg_rpm for other slot: got %.4f want 1.0000", resp.AvgRPM[otherSlotIndex])
	}
	if math.Abs(resp.AvgFirstTokenLatencyMs[otherSlotIndex]-300) > 0.0001 {
		t.Fatalf("unexpected avg_first_token_latency_ms for other slot: got %.4f want 300.0000", resp.AvgFirstTokenLatencyMs[otherSlotIndex])
	}
}

func TestGetMonitorHourlyPerformance_FiltersBySource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Local().Truncate(time.Hour)
	slotTime := now.Add(-time.Hour)

	h := newMonitorTestHandler(
		testUsageRecord(slotTime.Add(5*time.Minute), "api-1", "model-a", "source-a", false, 1000, 120),
		testUsageRecord(slotTime.Add(15*time.Minute), "api-1", "model-a", "source-b", false, 1100, 420),
	)

	rr := executeMonitorRequest(h.GetMonitorHourlyPerformance, "/monitor/hourly-performance?hours=6&granularity=hour&source=source-a")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots                  []string  `json:"slots"`
		AvgRPM                 []float64 `json:"avg_rpm"`
		AvgFirstTokenLatencyMs []float64 `json:"avg_first_token_latency_ms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	expectedSlot := slotTime.Format("2006-01-02T15:04:05-07:00")
	slotIndex := -1
	for i, slot := range resp.Slots {
		if slot == expectedSlot {
			slotIndex = i
			break
		}
	}
	if slotIndex < 0 {
		t.Fatalf("hour slot not found: %s", expectedSlot)
	}
	if math.Abs(resp.AvgRPM[slotIndex]-0.0166667) > 0.0001 {
		t.Fatalf("unexpected filtered avg_rpm: got %.4f want %.4f", resp.AvgRPM[slotIndex], 0.0166667)
	}
	if math.Abs(resp.AvgFirstTokenLatencyMs[slotIndex]-120) > 0.0001 {
		t.Fatalf("unexpected filtered avg_first_token_latency_ms: got %.4f want 120.0000", resp.AvgFirstTokenLatencyMs[slotIndex])
	}
}

func TestGetMonitorHourlyPerformance_RejectsUnsupportedWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := newMonitorTestHandler(testUsageRecord(time.Now(), "api-1", "model-a", "source-a", false, 1000, 200))
	rr := executeMonitorRequest(h.GetMonitorHourlyPerformance, "/monitor/hourly-performance?hours=48&granularity=minute")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestGetMonitorHourlyPerformance_RejectsMinuteGranularityForSevenDays(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := newMonitorTestHandler(testUsageRecord(time.Now(), "api-1", "model-a", "source-a", false, 1000, 200))
	rr := executeMonitorRequest(h.GetMonitorHourlyPerformance, "/monitor/hourly-performance?hours=168&granularity=minute")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestGetMonitorHourlyPerformance_HourGranularityAveragesRPMPerHour(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Local().Truncate(time.Hour)
	slotTime := now.Add(-time.Hour)

	h := newMonitorTestHandler(
		testUsageRecord(slotTime.Add(5*time.Minute), "api-1", "model-a", "source-a", false, 1000, 120),
		testUsageRecord(slotTime.Add(15*time.Minute), "api-1", "model-a", "source-a", false, 1100, 240),
		testUsageRecord(slotTime.Add(25*time.Minute), "api-1", "model-a", "source-a", true, 1200, 0),
	)

	rr := executeMonitorRequest(h.GetMonitorHourlyPerformance, "/monitor/hourly-performance?hours=6&granularity=hour")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots                  []string  `json:"slots"`
		AvgRPM                 []float64 `json:"avg_rpm"`
		AvgFirstTokenLatencyMs []float64 `json:"avg_first_token_latency_ms"`
		Granularity            string    `json:"granularity"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Granularity != "hour" {
		t.Fatalf("unexpected granularity: %s", resp.Granularity)
	}
	if len(resp.Slots) != 6 {
		t.Fatalf("unexpected slot count: got %d want 6", len(resp.Slots))
	}

	expectedSlot := slotTime.Format("2006-01-02T15:04:05-07:00")
	slotIndex := -1
	for i, slot := range resp.Slots {
		if slot == expectedSlot {
			slotIndex = i
			break
		}
	}
	if slotIndex < 0 {
		t.Fatalf("hour slot not found: %s", expectedSlot)
	}
	if math.Abs(resp.AvgRPM[slotIndex]-0.05) > 0.0001 {
		t.Fatalf("unexpected hourly avg_rpm: got %.4f want 0.0500", resp.AvgRPM[slotIndex])
	}
	if math.Abs(resp.AvgFirstTokenLatencyMs[slotIndex]-180) > 0.0001 {
		t.Fatalf("unexpected hourly avg_first_token_latency_ms: got %.4f want 180.0000", resp.AvgFirstTokenLatencyMs[slotIndex])
	}
}

func TestGetMonitorHourlyPerformance_UsesRequestedRangeEndAsAnchor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	anchor := time.Now().Local().Add(-24 * time.Hour).Truncate(time.Hour)
	slotTime := anchor.Add(-2 * time.Hour)
	start := anchor.Add(-5 * time.Hour)

	h := newMonitorTestHandler(
		testUsageRecord(slotTime.Add(10*time.Minute), "api-1", "model-a", "source-a", false, 1000, 180),
	)

	rr := executeMonitorRequest(
		h.GetMonitorHourlyPerformance,
		"/monitor/hourly-performance?hours=6&granularity=hour&start_time="+url.QueryEscape(start.Format(time.RFC3339))+"&end_time="+url.QueryEscape(anchor.Format(time.RFC3339)),
	)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots []string `json:"slots"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	expectedLastSlot := anchor.Format("2006-01-02T15:04:05-07:00")
	if got := resp.Slots[len(resp.Slots)-1]; got != expectedLastSlot {
		t.Fatalf("unexpected slot anchor: got %s want %s", got, expectedLastSlot)
	}
}

func TestGetMonitorHourlyPerformance_AcceptsSevenDayHourlyWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Local().Truncate(time.Hour)
	slotTime := now.Add(-24 * time.Hour)

	h := newMonitorTestHandler(
		testUsageRecord(slotTime.Add(10*time.Minute), "api-1", "model-a", "source-a", false, 1000, 180),
	)

	rr := executeMonitorRequest(h.GetMonitorHourlyPerformance, "/monitor/hourly-performance?hours=168&granularity=hour")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots       []string `json:"slots"`
		Granularity string   `json:"granularity"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Granularity != "hour" {
		t.Fatalf("unexpected granularity: %s", resp.Granularity)
	}
	if len(resp.Slots) != 168 {
		t.Fatalf("unexpected slot count: got %d want 168", len(resp.Slots))
	}
}
