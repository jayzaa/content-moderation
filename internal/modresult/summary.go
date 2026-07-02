// Package modresult turns the raw Alibaba Cloud Green ImageModeration
// response into a small, human-readable summary alongside the raw JSON.
package modresult

import (
	green20220302 "github.com/alibabacloud-go/green-20220302/v2/client"
)

// Summary is a simplified, human-friendly view of a moderation result.
type Summary struct {
	// Passed is true when no risky labels were found.
	Passed bool `json:"passed"`
	// RiskLevel is the overall risk assessment returned by the API
	// (e.g. "none", "low", "medium", "high").
	RiskLevel string `json:"riskLevel"`
	// Labels lists every risk label the service returned, if any.
	Labels []LabelResult `json:"labels"`
	// Message is a short, plain-English description of the result.
	Message string `json:"message"`
}

// LabelResult is one flagged risk category and its confidence/description.
type LabelResult struct {
	Label       string  `json:"label"`
	RiskLevel   string  `json:"riskLevel"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description,omitempty"`
}

// Summarize builds a Summary from the SDK's response body data.
func Summarize(data *green20220302.ImageModerationResponseBodyData) Summary {
	if data == nil {
		return Summary{Passed: true, RiskLevel: "none", Message: "No moderation data returned."}
	}

	overallRisk := "none"
	if data.RiskLevel != nil {
		overallRisk = *data.RiskLevel
	}

	var labels []LabelResult
	for _, r := range data.Result {
		if r == nil || r.Label == nil {
			continue
		}
		label := *r.Label
		if label == "" || label == "nonLabel" {
			continue // "nonLabel" means no risk detected for that scenario
		}
		lr := LabelResult{Label: label}
		if r.Confidence != nil {
			lr.Confidence = float64(*r.Confidence)
		}
		if r.RiskLevel != nil {
			lr.RiskLevel = *r.RiskLevel
		}
		if r.Description != nil {
			lr.Description = *r.Description
		}
		labels = append(labels, lr)
	}

	passed := len(labels) == 0 && (overallRisk == "none" || overallRisk == "")
	message := "No content risks detected."
	if !passed {
		message = "Potential content risk(s) detected: see labels."
	}

	return Summary{
		Passed:    passed,
		RiskLevel: overallRisk,
		Labels:    labels,
		Message:   message,
	}
}

// VideoTaskComplete reports whether an asynchronous video moderation task
// has finished processing. The API does not document an explicit "status"
// enum for in-progress vs. complete; empirically, a completed task has a
// non-nil overall RiskLevel in its Data, while a task still queued or
// processing does not yet have one.
func VideoTaskComplete(resp *green20220302.VideoModerationResultResponse) bool {
	return resp != nil && resp.Body != nil && resp.Body.Data != nil && resp.Body.Data.RiskLevel != nil
}

// VideoSummary is a simplified, human-friendly view of a video moderation
// result, combining frame (visual) and audio findings.
type VideoSummary struct {
	Passed      bool          `json:"passed"`
	RiskLevel   string        `json:"riskLevel"`
	FrameLabels []LabelResult `json:"frameLabels"`
	AudioLabels []LabelResult `json:"audioLabels"`
	Message     string        `json:"message"`
}

// SummarizeVideo builds a VideoSummary from a completed video moderation
// result's response body data.
func SummarizeVideo(data *green20220302.VideoModerationResultResponseBodyData) VideoSummary {
	if data == nil {
		return VideoSummary{Passed: true, RiskLevel: "none", Message: "No moderation data returned."}
	}

	overallRisk := "none"
	if data.RiskLevel != nil {
		overallRisk = *data.RiskLevel
	}

	var frameLabels []LabelResult
	if data.FrameResult != nil {
		for _, s := range data.FrameResult.FrameSummarys {
			if s == nil || s.Label == nil || *s.Label == "" || *s.Label == "nonLabel" {
				continue
			}
			lr := LabelResult{Label: *s.Label}
			if s.Description != nil {
				lr.Description = *s.Description
			}
			frameLabels = append(frameLabels, lr)
		}
	}

	var audioLabels []LabelResult
	if data.AudioResult != nil {
		for _, s := range data.AudioResult.AudioSummarys {
			if s == nil || s.Label == nil || *s.Label == "" || *s.Label == "nonLabel" {
				continue
			}
			lr := LabelResult{Label: *s.Label}
			if s.Description != nil {
				lr.Description = *s.Description
			}
			audioLabels = append(audioLabels, lr)
		}
	}

	passed := len(frameLabels) == 0 && len(audioLabels) == 0 && (overallRisk == "none" || overallRisk == "")
	message := "No content risks detected."
	if !passed {
		message = "Potential content risk(s) detected: see labels."
	}

	return VideoSummary{
		Passed:      passed,
		RiskLevel:   overallRisk,
		FrameLabels: frameLabels,
		AudioLabels: audioLabels,
		Message:     message,
	}
}
