package cloudapi

//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.
//
// Code generated by Alibaba Cloud SDK Code Generator.
// Changes may cause incorrect behavior and will be lost if the code is regenerated.

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/responses"
)

// SwitchApi invokes the cloudapi.SwitchApi API synchronously
// api document: https://help.aliyun.com/api/cloudapi/switchapi.html
func (client *Client) SwitchApi(request *SwitchApiRequest) (response *SwitchApiResponse, err error) {
	response = CreateSwitchApiResponse()
	err = client.DoAction(request, response)
	return
}

// SwitchApiWithChan invokes the cloudapi.SwitchApi API asynchronously
// api document: https://help.aliyun.com/api/cloudapi/switchapi.html
// asynchronous document: https://help.aliyun.com/document_detail/66220.html
func (client *Client) SwitchApiWithChan(request *SwitchApiRequest) (<-chan *SwitchApiResponse, <-chan error) {
	responseChan := make(chan *SwitchApiResponse, 1)
	errChan := make(chan error, 1)
	err := client.AddAsyncTask(func() {
		defer close(responseChan)
		defer close(errChan)
		response, err := client.SwitchApi(request)
		if err != nil {
			errChan <- err
		} else {
			responseChan <- response
		}
	})
	if err != nil {
		errChan <- err
		close(responseChan)
		close(errChan)
	}
	return responseChan, errChan
}

// SwitchApiWithCallback invokes the cloudapi.SwitchApi API asynchronously
// api document: https://help.aliyun.com/api/cloudapi/switchapi.html
// asynchronous document: https://help.aliyun.com/document_detail/66220.html
func (client *Client) SwitchApiWithCallback(request *SwitchApiRequest, callback func(response *SwitchApiResponse, err error)) <-chan int {
	result := make(chan int, 1)
	err := client.AddAsyncTask(func() {
		var response *SwitchApiResponse
		var err error
		defer close(result)
		response, err = client.SwitchApi(request)
		callback(response, err)
		result <- 1
	})
	if err != nil {
		defer close(result)
		callback(nil, err)
		result <- 0
	}
	return result
}

// SwitchApiRequest is the request struct for api SwitchApi
type SwitchApiRequest struct {
	*requests.RpcRequest
	GroupId        string `position:"Query" name:"GroupId"`
	ApiId          string `position:"Query" name:"ApiId"`
	StageName      string `position:"Query" name:"StageName"`
	Description    string `position:"Query" name:"Description"`
	HistoryVersion string `position:"Query" name:"HistoryVersion"`
}

// SwitchApiResponse is the response struct for api SwitchApi
type SwitchApiResponse struct {
	*responses.BaseResponse
	RequestId string `json:"RequestId" xml:"RequestId"`
}

// CreateSwitchApiRequest creates a request to invoke SwitchApi API
func CreateSwitchApiRequest() (request *SwitchApiRequest) {
	request = &SwitchApiRequest{
		RpcRequest: &requests.RpcRequest{},
	}
	request.InitWithApiInfo("CloudAPI", "2016-07-14", "SwitchApi", "apigateway", "openAPI")
	return
}

// CreateSwitchApiResponse creates a response to parse from SwitchApi response
func CreateSwitchApiResponse() (response *SwitchApiResponse) {
	response = &SwitchApiResponse{
		BaseResponse: &responses.BaseResponse{},
	}
	return
}