package agui

import (
	"net/http"

	"github.com/gin-gonic/gin"
	coreevents "github.com/saker-ai/saker/pkg/core/events"
)

func (g *Gateway) handleApprovalRespond(c *gin.Context) {
	runID := c.Param("runId")
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing runId"})
		return
	}

	var body struct {
		ApprovalID string `json:"approval_id"`
		Decision   string `json:"decision"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}

	pa := g.hitl.lookupApproval(runID)
	if pa == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no pending approval for this run"})
		return
	}
	if pa.ApprovalID != body.ApprovalID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "approval_id mismatch"})
		return
	}

	var decision coreevents.PermissionDecisionType
	switch body.Decision {
	case "allow":
		decision = coreevents.PermissionAllow
	default:
		decision = coreevents.PermissionDeny
	}

	select {
	case pa.ResultCh <- decision:
	default:
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (g *Gateway) handleQuestionRespond(c *gin.Context) {
	runID := c.Param("runId")
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing runId"})
		return
	}

	var body struct {
		QuestionID string            `json:"question_id"`
		Answers    map[string]string `json:"answers"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: " + err.Error()})
		return
	}

	pq := g.hitl.lookupQuestion(runID)
	if pq == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no pending question for this run"})
		return
	}
	if pq.QuestionID != body.QuestionID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "question_id mismatch"})
		return
	}

	answers := body.Answers
	if answers == nil {
		answers = map[string]string{}
	}

	select {
	case pq.ResultCh <- answers:
	default:
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
