package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type MessageType string

const (
	MsgStepStarted  MessageType = "step_started"
	MsgStepCompleted MessageType = "step_completed"
	MsgRunCompleted  MessageType = "run_completed"
	MsgLog           MessageType = "log"
	MsgError         MessageType = "error"
)

type StatusMessage struct {
	Type          MessageType `json:"type"`
	StepName      string      `json:"step_name,omitempty"`
	Result        string      `json:"result,omitempty"`
	Message       string      `json:"message,omitempty"`
	PromptText    string      `json:"prompt_text,omitempty"`
	AgentOutput   string      `json:"agent_output,omitempty"`
	AttemptNumber int         `json:"attempt_number,omitempty"`
	Timestamp     time.Time   `json:"timestamp"`
}

type StatusWriter struct {
	w   io.Writer
	enc *json.Encoder
}

func NewStatusWriter(w io.Writer) *StatusWriter {
	return &StatusWriter{w: w, enc: json.NewEncoder(w)}
}

func (s *StatusWriter) StepStarted(stepName string) {
	s.write(StatusMessage{Type: MsgStepStarted, StepName: stepName})
}

func (s *StatusWriter) StepCompleted(stepName, result string) {
	s.write(StatusMessage{Type: MsgStepCompleted, StepName: stepName, Result: result})
}

func (s *StatusWriter) RunCompleted(result string) {
	s.write(StatusMessage{Type: MsgRunCompleted, Result: result})
}

func (s *StatusWriter) Log(stepName, message string) {
	s.write(StatusMessage{Type: MsgLog, StepName: stepName, Message: message})
}

func (s *StatusWriter) Error(stepName, message string) {
	s.write(StatusMessage{Type: MsgError, StepName: stepName, Message: message})
}

func (s *StatusWriter) StepStartedWithPrompt(stepName, promptText string) {
	s.write(StatusMessage{Type: MsgStepStarted, StepName: stepName, PromptText: promptText})
}

func (s *StatusWriter) StepCompletedWithCapture(stepName, result, agentOutput string, attempt int) {
	s.write(StatusMessage{Type: MsgStepCompleted, StepName: stepName, Result: result, AgentOutput: agentOutput, AttemptNumber: attempt})
}

func (s *StatusWriter) write(msg StatusMessage) {
	msg.Timestamp = time.Now()
	_ = s.enc.Encode(msg)
}

func ParseStatusStream(data []byte) ([]StatusMessage, error) {
	var msgs []StatusMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var msg StatusMessage
		if err := dec.Decode(&msg); err != nil {
			return msgs, fmt.Errorf("failed to decode status message: %w", err)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}
