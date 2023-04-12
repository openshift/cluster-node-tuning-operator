package bugzilla

import (
	"encoding/json"
	"github.com/pkg/errors"
	"net/http"
	"net/url"
	"strconv"
)

const BUGZILLA_BASE_URL = "https://bugzilla.redhat.com"

type Bug struct {
	Id int `json:"id"`

	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type bugzillaResponse struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
	Total   int    `json:"total_matches"`
}

type searchResultFull struct {
	bugzillaResponse
	Bugs []Bug `json:"bugs"`
}

func getBugzilla(params *url.Values) ([]Bug, error) {
	searchResult := searchResultFull{}

	fullUrl := BUGZILLA_BASE_URL + "/rest/bug?" + params.Encode()

	req, err := http.NewRequest("GET", fullUrl, nil)
	if err != nil {
		return searchResult.Bugs, errors.Wrap(err, "Could not create bugzilla request")
	}
	ret, err := http.DefaultClient.Do(req)

	if err != nil {
		return searchResult.Bugs, errors.Wrap(err, "Could not query bugzilla")
	}
	defer ret.Body.Close()

	if ret.StatusCode != http.StatusOK {
		return nil, errors.Errorf("%d %s", ret.StatusCode, ret.Status)
	}

	decoder := json.NewDecoder(ret.Body)
	err = decoder.Decode(&searchResult)
	if err != nil {
		return searchResult.Bugs, errors.Wrap(err, "Could not query bugzilla")
	}

	if searchResult.Error {
		return searchResult.Bugs, errors.Errorf("search error: %s", searchResult.Message)
	}

	return searchResult.Bugs, nil
}

func RetrieveBug(bugId int) (*Bug, error) {
	params := url.Values{}
	params.Set("include_fields", "id,summary,status")
	params.Set("id", strconv.Itoa(bugId))

	bugs, err := getBugzilla(&params)
	if err != nil {
		return nil, err
	}

	if len(bugs) == 0 {
		return nil, errors.Errorf("no bugs with expected id rhbz#%d retrieved", bugId)
	}

	return &bugs[0], nil
}
