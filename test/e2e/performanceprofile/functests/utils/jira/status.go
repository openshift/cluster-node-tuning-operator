package jira

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	JIRA_BASE_URL = "https://issues.redhat.com"
	MAX_RETRIES   = 3
)

type JiraIssueStatusResponseFieldsStatus = struct {
	Name string `json:"name"`
}

type JiraIssueStatusResponseFields = struct {
	Status  JiraIssueStatusResponseFieldsStatus `json:"status"`
	Summary string                              `json:"summary"`
}

type JiraIssueStatusResponse = struct {
	Key    string                        `json:"key"`
	Fields JiraIssueStatusResponseFields `json:"fields"`
}

func RetrieveJiraStatus(key string) (*JiraIssueStatusResponse, error) {
	local_params := url.Values{}
	local_params.Set("fields", "summary,status")
	fullUrl := JIRA_BASE_URL + "/rest/api/2/issue/" + key + "?" + local_params.Encode()
	jiraResponse := JiraIssueStatusResponse{}
	for retryCount := 0; retryCount <= MAX_RETRIES; retryCount++ {
		req, err := http.NewRequest("GET", fullUrl, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create request for Jira status of %s", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to create client to get jira status of %s", key)
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			decoder := json.NewDecoder(resp.Body)
			err = decoder.Decode(&jiraResponse)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to decode jira status of %s", key)
			}
		case http.StatusTooManyRequests:
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				retryTime, err := convertRetryAfterTime(retryAfter)
				if err != nil {
					return nil, err
				}
				time.Sleep(retryTime)
			} else {
				delay := retryWithBackOff(retryCount)
				time.Sleep(delay)
			}
		default:
			return nil, errors.Errorf("failed to get jira status of %s: %d %s", key, resp.StatusCode, resp.Status)
		}
	}
	return &jiraResponse, nil
}

// retryWithBackOff use exponential backoff retries when we hit rate limit
func retryWithBackOff(attempts int) time.Duration {
	// we have a limit of 5 req/sec
	initialDelay := 200 * time.Millisecond
	maxDelay := 5 * time.Second
	delay := initialDelay * (1 << attempts)

	// add jitter
	jitter := time.Duration(rand.Int63n(int64(delay / 2)))
	delay += jitter

	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

// convertRetryAfterTime converts time in Retry-After in time.Duration
// the format can be in the form of date or seconds
func convertRetryAfterTime(retryAfter string) (time.Duration, error) {
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	if t, err := http.ParseTime(retryAfter); err != nil {
		wait := time.Until(t)
		if wait > 0 {
			return wait, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("Invalid Retry-After time value %s", retryAfter)
}
