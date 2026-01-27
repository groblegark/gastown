// Package main implements a proof-of-concept RPC server for Gas Town mobile access.
//
// This PoC demonstrates the architecture using HTTP/JSON endpoints that mirror
// the Connect-RPC service definitions. In production, this would use generated
// Connect-RPC handlers from the .proto files.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	port     = flag.Int("port", 8443, "Server port")
	townRoot = flag.String("town", "", "Town root directory (auto-detected if not set)")
)

func main() {
	flag.Parse()

	// Find town root
	root := *townRoot
	if root == "" {
		var err error
		root, err = workspace.FindFromCwdOrError()
		if err != nil {
			log.Fatalf("Not in a Gas Town workspace: %v", err)
		}
	}

	server := NewServer(root)

	// Register handlers using Connect-RPC URL patterns
	// Pattern: /package.Service/Method
	http.HandleFunc("/gastown.v1.StatusService/GetTownStatus", server.handleGetTownStatus)
	http.HandleFunc("/gastown.v1.StatusService/GetRigStatus", server.handleGetRigStatus)
	http.HandleFunc("/gastown.v1.MailService/ListInbox", server.handleListInbox)
	http.HandleFunc("/gastown.v1.DecisionService/ListPending", server.handleListPendingDecisions)

	// Health check
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Gas Town Mobile Server starting on %s", addr)
	log.Printf("Town root: %s", root)
	log.Printf("Endpoints:")
	log.Printf("  POST /gastown.v1.StatusService/GetTownStatus")
	log.Printf("  POST /gastown.v1.StatusService/GetRigStatus")
	log.Printf("  POST /gastown.v1.MailService/ListInbox")
	log.Printf("  POST /gastown.v1.DecisionService/ListPending")
	log.Printf("  GET  /health")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// Server holds the town context for handling requests.
type Server struct {
	townRoot string
	mu       sync.RWMutex
}

// NewServer creates a new mobile server instance.
func NewServer(townRoot string) *Server {
	return &Server{townRoot: townRoot}
}

// === Status Types (matching proto definitions) ===

type TownStatus struct {
	Name     string         `json:"name"`
	Location string         `json:"location"`
	Overseer *OverseerInfo  `json:"overseer,omitempty"`
	Agents   []AgentRuntime `json:"agents"`
	Rigs     []RigStatus    `json:"rigs"`
}

type OverseerInfo struct {
	Name       string `json:"name"`
	Email      string `json:"email,omitempty"`
	Username   string `json:"username,omitempty"`
	UnreadMail int    `json:"unread_mail"`
}

type AgentRuntime struct {
	Name         string `json:"name"`
	Address      string `json:"address"`
	Session      string `json:"session"`
	Role         string `json:"role"`
	Running      bool   `json:"running"`
	HasWork      bool   `json:"has_work"`
	WorkTitle    string `json:"work_title,omitempty"`
	HookBead     string `json:"hook_bead,omitempty"`
	State        string `json:"state,omitempty"`
	UnreadMail   int    `json:"unread_mail"`
	FirstSubject string `json:"first_subject,omitempty"`
}

type RigStatus struct {
	Name        string         `json:"name"`
	Polecats    []string       `json:"polecats"`
	Crews       []string       `json:"crews"`
	HasWitness  bool           `json:"has_witness"`
	HasRefinery bool           `json:"has_refinery"`
	Agents      []AgentRuntime `json:"agents,omitempty"`
}

// === Request/Response Types ===

type GetTownStatusRequest struct {
	Fast    bool `json:"fast"`
	Verbose bool `json:"verbose"`
}

type GetTownStatusResponse struct {
	Status *TownStatus `json:"status"`
}

type GetRigStatusRequest struct {
	RigName string `json:"rig_name"`
}

type GetRigStatusResponse struct {
	Status *RigStatus `json:"status"`
}

type ListInboxRequest struct {
	Address    string `json:"address"`
	UnreadOnly bool   `json:"unread_only"`
	Limit      int    `json:"limit"`
}

type ListInboxResponse struct {
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
	Unread   int       `json:"unread"`
}

type Message struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Timestamp string `json:"timestamp"`
	Read      bool   `json:"read"`
	Priority  string `json:"priority"`
}

type ListPendingDecisionsRequest struct {
	MinUrgency  string `json:"min_urgency"`
	RequestedBy string `json:"requested_by"`
}

type ListPendingDecisionsResponse struct {
	Decisions []Decision `json:"decisions"`
	Total     int        `json:"total"`
}

type Decision struct {
	ID          string           `json:"id"`
	Question    string           `json:"question"`
	Context     string           `json:"context"`
	Options     []DecisionOption `json:"options"`
	ChosenIndex int              `json:"chosen_index"`
	Rationale   string           `json:"rationale"`
	RequestedBy string           `json:"requested_by"`
	RequestedAt string           `json:"requested_at"`
	ResolvedBy  string           `json:"resolved_by"`
	ResolvedAt  string           `json:"resolved_at"`
	Urgency     string           `json:"urgency"`
	Blockers    []string         `json:"blockers"`
	Resolved    bool             `json:"resolved"`
}

type DecisionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended"`
}

// === Handlers ===

func (s *Server) handleGetTownStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req GetTownStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Empty body is OK, use defaults
		req = GetTownStatusRequest{}
	}

	status, err := s.collectTownStatus(req.Fast)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetTownStatusResponse{Status: status})
}

func (s *Server) handleGetRigStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req GetRigStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.RigName == "" {
		http.Error(w, "rig_name is required", http.StatusBadRequest)
		return
	}

	status, err := s.collectRigStatus(req.RigName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetRigStatusResponse{Status: status})
}

func (s *Server) handleListInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ListInboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req = ListInboxRequest{}
	}

	messages, total, unread, err := s.listInbox(req.Address, req.UnreadOnly, req.Limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListInboxResponse{
		Messages: messages,
		Total:    total,
		Unread:   unread,
	})
}

func (s *Server) handleListPendingDecisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ListPendingDecisionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req = ListPendingDecisionsRequest{}
	}

	decisions, err := s.listPendingDecisions(req.MinUrgency, req.RequestedBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ListPendingDecisionsResponse{
		Decisions: decisions,
		Total:     len(decisions),
	})
}

// === Data Collection ===

func (s *Server) collectTownStatus(fast bool) (*TownStatus, error) {
	// Load town config
	townConfigPath := constants.MayorTownPath(s.townRoot)
	townConfig, err := config.LoadTownConfig(townConfigPath)
	if err != nil {
		townConfig = &config.TownConfig{Name: filepath.Base(s.townRoot)}
	}

	// Load rigs config
	rigsConfigPath := constants.MayorRigsPath(s.townRoot)
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	// Create rig manager
	g := git.NewGit(s.townRoot)
	mgr := rig.NewManager(s.townRoot, rigsConfig, g)

	// Create tmux instance for runtime checks
	t := tmux.NewTmux()

	// Pre-fetch all tmux sessions
	allSessions := make(map[string]bool)
	if sessions, err := t.ListSessions(); err == nil {
		for _, sess := range sessions {
			allSessions[sess] = true
		}
	}

	// Discover rigs
	rigs, err := mgr.DiscoverRigs()
	if err != nil {
		return nil, fmt.Errorf("discovering rigs: %w", err)
	}

	// Load overseer info
	var overseerInfo *OverseerInfo
	if overseerConfig, err := config.LoadOrDetectOverseer(s.townRoot); err == nil && overseerConfig != nil {
		overseerInfo = &OverseerInfo{
			Name:     overseerConfig.Name,
			Email:    overseerConfig.Email,
			Username: overseerConfig.Username,
		}

		if !fast {
			mailRouter := mail.NewRouter(s.townRoot)
			if mailbox, err := mailRouter.GetMailbox("overseer"); err == nil {
				_, unread, _ := mailbox.Count()
				overseerInfo.UnreadMail = unread
			}
		}
	}

	// Build status
	status := &TownStatus{
		Name:     townConfig.Name,
		Location: s.townRoot,
		Overseer: overseerInfo,
		Rigs:     make([]RigStatus, 0, len(rigs)),
	}

	// Collect global agents (Mayor, Deacon)
	globalAgents := []struct {
		name    string
		session string
		role    string
	}{
		{"mayor", "gt-mayor", "mayor"},
		{"deacon", "gt-deacon", "deacon"},
	}

	for _, agent := range globalAgents {
		ar := AgentRuntime{
			Name:    agent.name,
			Address: agent.name + "/",
			Session: agent.session,
			Role:    agent.role,
			Running: allSessions[agent.session],
		}
		status.Agents = append(status.Agents, ar)
	}

	// Collect rig status
	for _, r := range rigs {
		rs := RigStatus{
			Name:        r.Name,
			Polecats:    r.Polecats,
			HasWitness:  r.HasWitness,
			HasRefinery: r.HasRefinery,
		}

		// Count crew workers using crew manager
		crewGit := git.NewGit(r.Path)
		crewMgr := crew.NewManager(r, crewGit)
		if workers, err := crewMgr.List(); err == nil {
			for _, w := range workers {
				rs.Crews = append(rs.Crews, w.Name)
			}
		}

		// Collect rig agents
		if r.HasWitness {
			session := fmt.Sprintf("gt-%s-witness", r.Name)
			rs.Agents = append(rs.Agents, AgentRuntime{
				Name:    "witness",
				Address: fmt.Sprintf("%s/witness", r.Name),
				Session: session,
				Role:    "witness",
				Running: allSessions[session],
			})
		}

		if r.HasRefinery {
			session := fmt.Sprintf("gt-%s-refinery", r.Name)
			rs.Agents = append(rs.Agents, AgentRuntime{
				Name:    "refinery",
				Address: fmt.Sprintf("%s/refinery", r.Name),
				Session: session,
				Role:    "refinery",
				Running: allSessions[session],
			})
		}

		// Add polecat agents
		for _, p := range r.Polecats {
			session := fmt.Sprintf("gt-%s-%s", r.Name, p)
			rs.Agents = append(rs.Agents, AgentRuntime{
				Name:    p,
				Address: fmt.Sprintf("%s/polecats/%s", r.Name, p),
				Session: session,
				Role:    "polecat",
				Running: allSessions[session],
			})
		}

		// Add crew agents
		for _, c := range rs.Crews {
			session := fmt.Sprintf("gt-%s-crew-%s", r.Name, c)
			rs.Agents = append(rs.Agents, AgentRuntime{
				Name:    c,
				Address: fmt.Sprintf("%s/crew/%s", r.Name, c),
				Session: session,
				Role:    "crew",
				Running: allSessions[session],
			})
		}

		status.Rigs = append(status.Rigs, rs)
	}

	return status, nil
}

func (s *Server) collectRigStatus(rigName string) (*RigStatus, error) {
	status, err := s.collectTownStatus(false)
	if err != nil {
		return nil, err
	}

	for _, r := range status.Rigs {
		if r.Name == rigName {
			return &r, nil
		}
	}

	return nil, fmt.Errorf("rig not found: %s", rigName)
}

func (s *Server) listInbox(address string, unreadOnly bool, limit int) ([]Message, int, int, error) {
	mailRouter := mail.NewRouter(s.townRoot)

	// Default to overseer inbox
	if address == "" {
		address = "overseer"
	}

	mailbox, err := mailRouter.GetMailbox(address)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("getting mailbox: %w", err)
	}

	total, unread, err := mailbox.Count()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("counting mail: %w", err)
	}

	msgs, err := mailbox.List()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("listing mail: %w", err)
	}

	var messages []Message
	for _, m := range msgs {
		if unreadOnly && m.Read {
			continue
		}

		messages = append(messages, Message{
			ID:        m.ID,
			From:      m.From,
			To:        m.To,
			Subject:   m.Subject,
			Body:      m.Body,
			Timestamp: m.Timestamp.Format("2006-01-02T15:04:05Z"),
			Read:      m.Read,
			Priority:  string(m.Priority),
		})

		if limit > 0 && len(messages) >= limit {
			break
		}
	}

	return messages, total, unread, nil
}

func (s *Server) listPendingDecisions(minUrgency, requestedBy string) ([]Decision, error) {
	townBeadsPath := beads.GetTownBeadsPath(s.townRoot)
	client := beads.New(townBeadsPath)

	// List pending decision beads using the dedicated API
	issues, err := client.ListDecisions()
	if err != nil {
		return nil, fmt.Errorf("listing decisions: %w", err)
	}

	var decisions []Decision
	for _, issue := range issues {
		fields := beads.ParseDecisionFields(issue.Description)
		if fields == nil {
			continue
		}

		// Filter by urgency if specified
		if minUrgency != "" && urgencyLevel(fields.Urgency) < urgencyLevel(minUrgency) {
			continue
		}

		// Filter by requester if specified
		if requestedBy != "" && fields.RequestedBy != requestedBy {
			continue
		}

		var options []DecisionOption
		for _, opt := range fields.Options {
			options = append(options, DecisionOption{
				Label:       opt.Label,
				Description: opt.Description,
				Recommended: opt.Recommended,
			})
		}

		decisions = append(decisions, Decision{
			ID:          issue.ID,
			Question:    fields.Question,
			Context:     fields.Context,
			Options:     options,
			ChosenIndex: fields.ChosenIndex,
			Rationale:   fields.Rationale,
			RequestedBy: fields.RequestedBy,
			RequestedAt: fields.RequestedAt,
			ResolvedBy:  fields.ResolvedBy,
			ResolvedAt:  fields.ResolvedAt,
			Urgency:     fields.Urgency,
			Blockers:    fields.Blockers,
			Resolved:    fields.ChosenIndex > 0,
		})
	}

	return decisions, nil
}

func urgencyLevel(u string) int {
	switch u {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
