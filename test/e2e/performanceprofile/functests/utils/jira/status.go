package jira

import (
	"encoding/json"
	"github.com/pkg/errors"
	"net/http"
	"net/url"
)

const JIRA_BASE_URL = "https://issues.redhat.com"

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
	ret, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create client to get jira status of %s", key)
	}
	defer ret.Body.Close()

	if ret.StatusCode != http.StatusOK {
		return nil, errors.Errorf("failed to get jira status of %s: %d %s", key, ret.StatusCode, ret.Status)
	}

	response := JiraIssueStatusResponse{}
	decoder := json.NewDecoder(ret.Body)
	err = decoder.Decode(&response)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode jira status of %s", key)
	}

	return &response, nil
}
