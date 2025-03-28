package jira

import (
	"encoding/json"
	"github.com/pkg/errors"
	"math/rand"
	"net/http"
	"net/url"
	"time"
)

const JIRA_BASE_URL = "https://issues.redhat.com"
const MAX_RETRIES = 3

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
	req, err := http.NewRequest("GET", fullUrl, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create request for Jira status of %s", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create client to get jira status of %s", key)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests {
			for retryCount := 0; retryCount <= MAX_RETRIES; retryCount++ {
				delay := retryWithBackOff(retryCount)
				time.Sleep(delay)
				resp, err = retryRequest(req)
				if err != nil {
					return nil, errors.Wrapf(err, "failed to create request for Jira status of %s", key)
				}
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
			}
		} else {
			return nil, errors.Errorf("failed to get jira status of %s: %d %s", key, resp.StatusCode, resp.Status)
		}
	}
	jiraResponse := JiraIssueStatusResponse{}
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&jiraResponse)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode jira status of %s", key)
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

// retryRequest creates new http request
func retryRequest(req *http.Request) (*http.Response, error) {
	newReq, err := http.NewRequest("GET", req.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	// copy headers
	newReq.Header = req.Header.Clone()
	resp, err := http.DefaultClient.Do(newReq)
	return resp, err
}
