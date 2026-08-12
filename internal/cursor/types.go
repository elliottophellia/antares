package cursor

import "encoding/json"

type Me struct {
	APIKeyName    string `json:"apiKeyName"`
	CreatedAt     string `json:"createdAt"`
	UserID        int64  `json:"userId,omitempty"`
	UserEmail     string `json:"userEmail,omitempty"`
	UserFirstName string `json:"userFirstName,omitempty"`
	UserLastName  string `json:"userLastName,omitempty"`
}

type ModelParameterValue struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName,omitempty"`
}

type ModelParameter struct {
	ID          string                `json:"id"`
	DisplayName string                `json:"displayName,omitempty"`
	Values      []ModelParameterValue `json:"values"`
}

type ModelParameterSelection struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type ModelVariant struct {
	Params      []ModelParameterSelection `json:"params"`
	DisplayName string                    `json:"displayName"`
	Description string                    `json:"description,omitempty"`
	IsDefault   bool                      `json:"isDefault,omitempty"`
}

type Model struct {
	ID          string           `json:"id"`
	DisplayName string           `json:"displayName"`
	Description string           `json:"description,omitempty"`
	Aliases     []string         `json:"aliases,omitempty"`
	Parameters  []ModelParameter `json:"parameters,omitempty"`
	Variants    []ModelVariant   `json:"variants,omitempty"`
}

type ModelCatalog struct {
	Items []Model `json:"items"`
}

type StreamEvent struct {
	ID       string
	Type     string
	Status   string
	Text     string
	ToolName string
	Raw      json.RawMessage
}
