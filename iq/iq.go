// Copyright 2020 Sonatype Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Definitions and functions for processing golang purls with Nexus IQ Server
package iq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/sonatype-nexus-community/nancy/buildversion"
	"github.com/sonatype-nexus-community/nancy/configuration"
	"github.com/sonatype-nexus-community/nancy/cyclonedx"
)

var IQ_SERVER_BASE_URL = "http://localhost:8070"

const INTERNAL_APPLICATION_ID_URL = "/api/v2/applications?publicId="

const THIRD_PARTY_API_LEFT = "/api/v2/scan/applications/"

const THIRD_PARTY_API_RIGHT = "/sources/nancy?stageId="

const (
	pollInterval = 1 * time.Second
)

type ApplicationResponse struct {
	Applications []Application `json:"applications"`
}

type Application struct {
	ID string `json:"id"`
}

type ThirdPartyAPIResult struct {
	StatusURL string `json:"statusUrl"`
}

type StatusURLResult struct {
	PolicyAction  string `json:"policyAction"`
	ReportHtmlURL string `json:"reportHtmlUrl"`
	IsError       bool   `json:"isError"`
	ErrorMessage  string `json:"errorMessage"`
}

var LOCAL_CONFIG configuration.Configuration

var statusUrlResp StatusURLResult

var stillPolling bool = true

func AuditPackages(purls []string, applicationID string, config configuration.Configuration) StatusURLResult {
	LOCAL_CONFIG = config
	internalID := getInternalApplicationID(applicationID)
	sbom := cyclonedx.ProcessPurlsIntoSBOM(purls)
	statusURL := submitToThirdPartyAPI(sbom, internalID)

	finished := make(chan bool)

	statusUrlResp = StatusURLResult{}

	go func() {
		for {
			select {
			case <-finished:
				return
			default:
				pollIQServer(fmt.Sprintf("%s/%s", IQ_SERVER_BASE_URL, statusURL), finished)
				time.Sleep(time.Second * 1)
			}
		}
	}()

	<-finished
	return statusUrlResp
}

func getInternalApplicationID(applicationID string) (internalID string) {
	client := &http.Client{}

	req, err := http.NewRequest(
		"GET",
		fmt.Sprintf("%s%s%s", IQ_SERVER_BASE_URL, INTERNAL_APPLICATION_ID_URL, applicationID),
		nil,
	)

	req.SetBasicAuth(LOCAL_CONFIG.User, LOCAL_CONFIG.Token)

	resp, err := client.Do(req)
	if err != nil {
		fmt.Print(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Print(err)
		}
		var response ApplicationResponse
		json.Unmarshal(bodyBytes, &response)
		return response.Applications[0].ID
	}
	return ""
}

func submitToThirdPartyAPI(sbom string, internalID string) string {
	client := &http.Client{}

	url := fmt.Sprintf("%s%s", IQ_SERVER_BASE_URL, fmt.Sprintf("%s%s%s%s", THIRD_PARTY_API_LEFT, internalID, THIRD_PARTY_API_RIGHT, LOCAL_CONFIG.Stage))

	req, err := http.NewRequest(
		"POST",
		url,
		bytes.NewBuffer([]byte(sbom)),
	)

	req.SetBasicAuth(LOCAL_CONFIG.User, LOCAL_CONFIG.Token)

	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("User-Agent", fmt.Sprintf("nancy-client/%s", buildversion.BuildVersion))

	resp, err := client.Do(req)
	if err != nil {
		fmt.Print(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Print(err)
		}
		var response ThirdPartyAPIResult
		json.Unmarshal(bodyBytes, &response)
		return response.StatusURL
	}

	return ""
}

func pollIQServer(statusURL string, finished chan bool) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", statusURL, nil)

	req.SetBasicAuth(LOCAL_CONFIG.User, LOCAL_CONFIG.Token)

	req.Header.Set("User-Agent", fmt.Sprintf("nancy-client/%s", buildversion.BuildVersion))

	resp, err := client.Do(req)

	if err != nil {
		finished <- true
		fmt.Print(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Print(err)
		}
		var response StatusURLResult
		json.Unmarshal(bodyBytes, &response)
		statusUrlResp = response
		if response.IsError {
			finished <- true
		}
		finished <- true
	}
	fmt.Print(".")
}
