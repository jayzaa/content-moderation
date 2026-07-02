// Package moderation wraps the Alibaba Cloud Content Moderation (Green/CIP)
// image moderation API (2022-03-02) for use by the image-detection service.
//
// Credentials and region are provided explicitly by the caller (see
// internal/config), which sources them from environment variables /
// a .env file. No secrets are read directly from disk by this package.
package moderation

import (
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	green20220302 "github.com/alibabacloud-go/green-20220302/v2/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
)

// endpointForRegion returns the Green/CIP public endpoint for a region.
// See: https://help.aliyun.com/en/document_detail/467829.html
func endpointForRegion(region string) string {
	return fmt.Sprintf("green-cip.%s.aliyuncs.com", region)
}

// NewClient builds an authenticated Green (content moderation) API client
// from an explicit AccessKey ID/secret and region.
func NewClient(accessKeyID, accessKeySecret, region string) (*green20220302.Client, error) {
	if accessKeyID == "" || accessKeySecret == "" {
		return nil, fmt.Errorf("moderation: accessKeyID and accessKeySecret are required")
	}
	if region == "" {
		return nil, fmt.Errorf("moderation: region is required")
	}

	config := &openapi.Config{
		AccessKeyId:     tea.String(accessKeyID),
		AccessKeySecret: tea.String(accessKeySecret),
		Endpoint:        tea.String(endpointForRegion(region)),
	}

	client, err := green20220302.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("moderation: create client: %w", err)
	}
	return client, nil
}

// ModerateImageURL submits a publicly accessible image URL for moderation
// using the "postImageCheckByVL_global" service (Image Moderation for
// Large and Small Model Integration, Global Edition) and returns the raw
// API response.
func ModerateImageURL(client *green20220302.Client, imageURL string) (*green20220302.ImageModerationResponse, error) {
	serviceParams := util.ToJSONString(map[string]string{
		"imageUrl": imageURL,
	})

	request := &green20220302.ImageModerationRequest{
		Service:           tea.String("postImageCheckByVL_global"),
		ServiceParameters: serviceParams,
	}

	resp, err := client.ImageModeration(request)
	if err != nil {
		return nil, fmt.Errorf("moderation: ImageModeration call: %w", err)
	}
	return resp, nil
}

// videoService is the video moderation service code used for both
// submission and result polling. "videoDetection_global" is the
// documented international-region video file detection service (see
// https://www.alibabacloud.com/help/en/content-moderation/latest/video-audit-enhanced-edition-introduction-and-billing-description).
//
// Previously tried and rejected:
//   - "videoDetection_cb": valid service, but rejected with
//     "commodityCode is invalid:lvwang_cip_public_cn" (product not
//     activated/subscribed on the test account).
//   - "videoDetectionByVL": not a real service code at all
//     ("service is invalid:videoDetectionByVL").
const videoService = "videoDetection_global"

// SubmitVideoURL creates an asynchronous video moderation task for a
// publicly accessible video URL and returns the resulting task ID.
func SubmitVideoURL(client *green20220302.Client, videoURL string) (taskID string, err error) {
	serviceParams := util.ToJSONString(map[string]string{
		"url": videoURL,
	})

	request := &green20220302.VideoModerationRequest{
		Service:           tea.String(videoService),
		ServiceParameters: serviceParams,
	}

	resp, err := client.VideoModeration(request)
	if err != nil {
		return "", fmt.Errorf("moderation: VideoModeration call: %w", err)
	}
	if resp == nil || resp.Body == nil || resp.Body.Data == nil || resp.Body.Data.TaskId == nil {
		errMsg := "no task ID returned"
		if resp != nil && resp.Body != nil && resp.Body.Message != nil {
			errMsg = *resp.Body.Message
		}
		return "", fmt.Errorf("moderation: VideoModeration failed: %s", errMsg)
	}
	return *resp.Body.Data.TaskId, nil
}

// PollVideoResult retrieves the current status/result of a previously
// submitted video moderation task. Callers should poll this periodically
// until the task completes (see modresult.VideoTaskComplete).
func PollVideoResult(client *green20220302.Client, taskID string) (*green20220302.VideoModerationResultResponse, error) {
	serviceParams := util.ToJSONString(map[string]string{
		"taskId": taskID,
	})

	request := &green20220302.VideoModerationResultRequest{
		Service:           tea.String(videoService),
		ServiceParameters: serviceParams,
	}

	resp, err := client.VideoModerationResult(request)
	if err != nil {
		return nil, fmt.Errorf("moderation: VideoModerationResult call: %w", err)
	}
	return resp, nil
}
