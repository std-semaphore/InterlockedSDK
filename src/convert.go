package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

func convertCmd() *cobra.Command {
	var dir string
	var out string
	var diagramFilter string

	cmd := &cobra.Command{
		Use:   "convert [path]",
		Short: "Convert a timetable directory into a raw JSON file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				dir, _ = os.Getwd()
			}

			doc, err := buildOutput(dir, diagramFilter)
			if err != nil {
				return err
			}

			data, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal output: %w", err)
			}

			if out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", out, err)
				}
				fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", out)
				return nil
			}

			fmt.Println(string(data))
			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Timetable directory (default: current directory)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Output file (default: stdout)")
	cmd.Flags().StringVar(&diagramFilter, "diagram", "", "Only convert a single diagram (e.g. 2K)")
	return cmd
}

type OutputDoc struct {
	Manifest      *Manifest           `json:"manifest"`
	Tiplocs       []TiplocOut         `json:"tiplocs"`
	Paths         []PathOut           `json:"paths"`
	Consists      []ConsistOut        `json:"consists"`
	Connections   map[string][]string `json:"connections"`
	FringeWeights map[string]float64  `json:"fringe_weights"`
	Stations      []StationOut        `json:"stations"`
	Diagrams      []DiagramOut        `json:"diagrams"`
}

type ManifestOut struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Author     string `json:"author"`
	Game       string `json:"game"`
	Sim        string `json:"sim"`
	SimVersion string `json:"sim_version"`
}

type TiplocOut struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	CRS     string  `json:"crs,omitempty"`
	Section string  `json:"section,omitempty"`
	At      float64 `json:"at,omitempty"`
	NoRev   bool    `json:"noRev,omitempty"`
	Object  string  `json:"object,omitempty"`
}

type PathOut struct {
	ID          string `json:"id"`
	FromSection string `json:"from_section"`
	ToSection   string `json:"to_section"`
	FromAt      *int   `json:"from_at,omitempty"`
	ToAt        *int   `json:"to_at,omitempty"`
}

type ActivityRangeOut struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type ConsistOut struct {
	ID          string                      `json:"id"`
	Description string                      `json:"description"`
	Units       []string                    `json:"units"`
	Activities  map[string]ActivityRangeOut `json:"activities,omitempty"`
}

type StationOut struct {
	CRS        string `json:"crs"`
	Name       string `json:"name"`
	PlatLength int    `json:"plat_length,omitempty"`
}

type AllocEntryOut struct {
	Consist string  `json:"consist,omitempty"`
	Diagram string  `json:"diagram,omitempty"`
	Weight  float64 `json:"weight,omitempty"`
}

type ScenarioOut struct {
	BaseDelay      float64  `json:"base_delay,omitempty"`
	DelayedPct     float64  `json:"delayed_pct,omitempty"`
	DisruptionPct  float64  `json:"disruption_pct,omitempty"`
	SetSwapPct     float64  `json:"set_swap_pct,omitempty"`
	RunsAsRequired *float64 `json:"runs_as_required,omitempty"`
}

type EntryOut struct {
	Type      string  `json:"type"`
	Section   string  `json:"section,omitempty"`
	At        float64 `json:"at,omitempty"`
	Direction string  `json:"direction,omitempty"`
}

type ExitOut struct {
	Type string `json:"type"`
}

type ActivityOut struct {
	Type           string   `json:"type"`
	TargetHeadcode string   `json:"targetHeadcode,omitempty"`
	TargetUnit     *int     `json:"targetUnit,omitempty"`
	TargetDiagram  string   `json:"targetDiagram,omitempty"`
	Forms          string   `json:"forms,omitempty"`
	Consists       []string `json:"consists,omitempty"`
}

type TimetableEntryOut struct {
	Type                string        `json:"type"`
	CRS                 string        `json:"crs,omitempty"`
	Tiploc              string        `json:"tiploc,omitempty"`
	Arr                 string        `json:"arr,omitempty"`
	Dep                 string        `json:"dep,omitempty"`
	Plat                string        `json:"plat,omitempty"`
	Pass                bool          `json:"pass,omitempty"`
	Path                string        `json:"path,omitempty"`
	StopPct             *float64      `json:"stop_pct,omitempty"`
	AllowEarly          *bool         `json:"allow_early,omitempty"`
	SignallerPermission bool          `json:"signaller_permission,omitempty"`
	Activities          []ActivityOut `json:"activities,omitempty"`
}

type ServiceOut struct {
	Headcode       string              `json:"headcode"`
	Diagram        string              `json:"diagram"`
	EntryTime      string              `json:"entry_time"`
	TimingLoad     string              `json:"timing_load,omitempty"`
	DoNotAdvertise bool                `json:"do_not_advertise,omitempty"`
	AllowEarly     *bool               `json:"allow_early,omitempty"`
	Scenario       *ScenarioOut        `json:"scenario,omitempty"`
	Entry          EntryOut            `json:"entry"`
	Exit           ExitOut             `json:"exit"`
	Timetable      []TimetableEntryOut `json:"timetable"`
}

type DiagramOut struct {
	ID             string          `json:"id"`
	Operator       string          `json:"operator"`
	Allocation     []AllocEntryOut `json:"allocation"`
	SetSwapPool    []AllocEntryOut `json:"set_swap_pool,omitempty"`
	DoNotAdvertise bool            `json:"do_not_advertise,omitempty"`
	AllowEarly     *bool           `json:"allow_early,omitempty"`
	Scenario       *ScenarioOut    `json:"scenario,omitempty"`
	Services       []ServiceOut    `json:"services"`
}

type rawManifest struct {
	ID         string `toml:"id"`
	Name       string `toml:"name"`
	Version    string `toml:"version"`
	Author     string `toml:"author"`
	Game       string `toml:"game"`
	Sim        string `toml:"sim"`
	SimVersion string `toml:"sim_version"`
}

type allocationEntry struct {
	Consist string  `toml:"consist"`
	Diagram string  `toml:"diagram"`
	Weight  float64 `toml:"weight"`
}

type rawScenario struct {
	BaseDelay      float64  `toml:"base_delay"`
	DelayedPct     float64  `toml:"delayed_pct"`
	DisruptionPct  float64  `toml:"disruption_pct"`
	SetSwapPct     float64  `toml:"set_swap_pct"`
	RunsAsRequired *float64 `toml:"runs_as_required"`
}

type rawDiagram struct {
	Diagram struct {
		Operator       string            `toml:"operator"`
		Allocation     []allocationEntry `toml:"allocation"`
		SetSwapPool    []allocationEntry `toml:"set_swap_pool"`
		DoNotAdvertise *bool             `toml:"do_not_advertise"`
		AllowEarly     *bool             `toml:"allow_early"`
	} `toml:"diagram"`
	Scenario *rawScenario `toml:"scenario"`
	Service  []rawService `toml:"service"`
}

type rawActivityConfig struct {
	Tiploc        string   `toml:"tiploc"`
	Occurrence    int      `toml:"occurrence"`
	DetachUnit    *int     `toml:"detach_unit"`
	Forms         string   `toml:"forms"`
	DetachDiagram string   `toml:"detach_diagram"`
	Consists      []string `toml:"consists"`
	Joins         string   `toml:"joins"`
	AttachUnit    *int     `toml:"attach_unit"`
}

type rawServiceActivities struct {
	Attach     []rawActivityConfig `toml:"attach"`
	Detach     []rawActivityConfig `toml:"detach"`
	Reverse    []rawActivityConfig `toml:"reverse"`
	CrewChange []rawActivityConfig `toml:"crew_change"`
}

type rawService struct {
	Headcode       string               `toml:"headcode"`
	Template       string               `toml:"template"`
	Departs        string               `toml:"departs"`
	Enters         string               `toml:"enters"`
	Exits          string               `toml:"exits"`
	TimingRef      string               `toml:"-"`
	Occurrence     int                  `toml:"occurrence"`
	Static         *rawStaticRef        `toml:"static"`
	Timing         *rawServiceTiming    `toml:"-"`
	Exception      []rawSimException    `toml:"exception"`
	Activity       rawServiceActivities `toml:"activity"`
	Recurrence     *rawRecurrence       `toml:"recurrence"`
	TiplocTiming   map[string]string    `toml:"-"`
	DoNotAdvertise *bool                `toml:"do_not_advertise"`
	AllowEarly     *bool                `toml:"allow_early"`

	BaseDelay      *float64 `toml:"base_delay"`
	DelayedPct     *float64 `toml:"delayed_pct"`
	DisruptionPct  *float64 `toml:"disruption_pct"`
	SetSwapPct     *float64 `toml:"set_swap_pct"`
	RunsAsRequired *float64 `toml:"runs_as_required"`
}

type rawServiceTiming struct {
	Profile string `toml:"profile"`
}

func (s *rawService) UnmarshalTOML(data interface{}) error {
	m, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Errorf("service: expected table")
	}

	s.TiplocTiming = map[string]string{}
	s.Headcode = stringValue(m["headcode"])
	s.Template = stringValue(m["template"])
	s.Departs = stringValue(m["departs"])
	s.Enters = stringValue(m["enters"])
	s.Exits = stringValue(m["exits"])
	s.TimingRef = ""
	s.Occurrence = intValue(m["occurrence"])
	s.DoNotAdvertise = boolPtrValue(m["do_not_advertise"])
	s.AllowEarly = boolPtrValue(m["allow_early"])
	s.BaseDelay = floatPtrValue(m["base_delay"])
	s.DelayedPct = floatPtrValue(m["delayed_pct"])
	s.DisruptionPct = floatPtrValue(m["disruption_pct"])
	s.SetSwapPct = floatPtrValue(m["set_swap_pct"])
	s.RunsAsRequired = floatPtrValue(m["runs_as_required"])

	if timing, ok := m["timing"]; ok {
		switch v := timing.(type) {
		case string:
			s.TimingRef = v
		case map[string]interface{}:
			s.Timing = &rawServiceTiming{Profile: stringValue(v["profile"])}
		}
	}
	if st, ok := m["static"].(map[string]interface{}); ok {
		s.Static = parseStaticRef(st)
	}
	if rec, ok := m["recurrence"].(map[string]interface{}); ok {
		s.Recurrence = &rawRecurrence{
			Every:             stringValue(rec["every"]),
			Until:             stringValue(rec["until"]),
			HeadcodeIncrement: intValue(rec["headcode_increment"]),
			HeadcodeList:      stringSliceValue(rec["headcode_list"]),
		}
	}
	if exs, ok := m["exception"].([]map[string]interface{}); ok {
		for _, ex := range exs {
			s.Exception = append(s.Exception, parseSimException(ex))
		}
	} else if exs, ok := m["exception"].([]interface{}); ok {
		for _, item := range exs {
			if ex, ok := item.(map[string]interface{}); ok {
				s.Exception = append(s.Exception, parseSimException(ex))
			}
		}
	}
	if act, ok := m["activity"].(map[string]interface{}); ok {
		s.Activity = parseServiceActivities(act)
	}

	known := map[string]bool{
		"headcode": true, "template": true, "departs": true, "enters": true, "exits": true,
		"timing": true, "occurrence": true, "static": true, "exception": true, "activity": true,
		"recurrence": true, "do_not_advertise": true, "allow_early": true,
		"base_delay": true, "delayed_pct": true, "disruption_pct": true, "set_swap_pct": true,
		"runs_as_required": true,
	}
	for k, v := range m {
		if known[k] {
			continue
		}
		if ref, ok := v.(string); ok {
			s.TiplocTiming[k] = ref
		}
	}

	return nil
}

func parseStaticRef(m map[string]interface{}) *rawStaticRef {
	out := &rawStaticRef{
		Template: stringValue(m["template"]),
		Reversed: boolValue(m["reversed"]),
	}
	if exs, ok := m["exception"].([]map[string]interface{}); ok {
		for _, ex := range exs {
			out.Exception = append(out.Exception, parseStaticException(ex))
		}
	} else if exs, ok := m["exception"].([]interface{}); ok {
		for _, item := range exs {
			if ex, ok := item.(map[string]interface{}); ok {
				out.Exception = append(out.Exception, parseStaticException(ex))
			}
		}
	}
	return out
}

func parseStaticException(m map[string]interface{}) rawStaticException {
	return rawStaticException{
		CRS:        stringValue(m["crs"]),
		Exclude:    boolValue(m["exclude"]),
		TravelTime: stringValue(m["travel_time"]),
		Dwell:      stringValue(m["dwell"]),
		Occurrence: intValue(m["occurrence"]),
	}
}

func parseSimException(m map[string]interface{}) rawSimException {
	return rawSimException{
		Tiploc:              stringValue(m["tiploc"]),
		Occurrence:          intValue(m["occurrence"]),
		TravelTime:          stringValue(m["travel_time"]),
		Allowance:           stringValue(m["allowance"]),
		Dwell:               stringValue(m["dwell"]),
		Pass:                boolPtrValue(m["pass"]),
		StopPct:             floatPtrValue(m["stop_pct"]),
		Plat:                m["plat"],
		Path:                stringValue(m["path"]),
		AllowEarly:          boolPtrValue(m["allow_early"]),
		SignallerPermission: boolPtrValue(m["signaller_permission"]),
	}
}

func parseServiceActivities(m map[string]interface{}) rawServiceActivities {
	return rawServiceActivities{
		Attach:     parseActivityList(m["attach"]),
		Detach:     parseActivityList(m["detach"]),
		Reverse:    parseActivityList(m["reverse"]),
		CrewChange: parseActivityList(m["crew_change"]),
	}
}

func parseActivityList(v interface{}) []rawActivityConfig {
	var out []rawActivityConfig
	switch items := v.(type) {
	case []map[string]interface{}:
		for _, item := range items {
			out = append(out, parseActivityConfig(item))
		}
	case []interface{}:
		for _, item := range items {
			if m, ok := item.(map[string]interface{}); ok {
				out = append(out, parseActivityConfig(m))
			}
		}
	case map[string]interface{}:
		out = append(out, parseActivityConfig(items))
	}
	return out
}

func parseActivityConfig(m map[string]interface{}) rawActivityConfig {
	return rawActivityConfig{
		Tiploc:        stringValue(m["tiploc"]),
		Occurrence:    intValue(m["occurrence"]),
		DetachUnit:    intPtrValue(m["detach_unit"]),
		Forms:         stringValue(m["forms"]),
		DetachDiagram: stringValue(m["detach_diagram"]),
		Consists:      stringSliceValue(m["consists"]),
		Joins:         stringValue(m["joins"]),
		AttachUnit:    intPtrValue(m["attach_unit"]),
	}
}

func stringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolValue(v interface{}) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func boolPtrValue(v interface{}) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

func intValue(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func intPtrValue(v interface{}) *int {
	switch n := v.(type) {
	case int:
		return &n
	case int64:
		x := int(n)
		return &x
	case float64:
		x := int(n)
		return &x
	default:
		return nil
	}
}

func floatPtrValue(v interface{}) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case int:
		x := float64(n)
		return &x
	case int64:
		x := float64(n)
		return &x
	default:
		return nil
	}
}

func stringSliceValue(v interface{}) []string {
	switch items := v.(type) {
	case []string:
		return items
	case []interface{}:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

type rawStaticRef struct {
	Template  string               `toml:"template"`
	Reversed  bool                 `toml:"reversed"`
	Exception []rawStaticException `toml:"exception"`
}

type rawStaticException struct {
	CRS        string `toml:"crs"`
	Exclude    bool   `toml:"exclude"`
	TravelTime string `toml:"travel_time"`
	Dwell      string `toml:"dwell"`
	Occurrence int    `toml:"occurrence"`
}

type rawSimException struct {
	Tiploc              string      `toml:"tiploc"`
	Occurrence          int         `toml:"occurrence"`
	TravelTime          string      `toml:"travel_time"`
	Allowance           string      `toml:"allowance"`
	Dwell               string      `toml:"dwell"`
	Pass                *bool       `toml:"pass"`
	StopPct             *float64    `toml:"stop_pct"`
	Plat                interface{} `toml:"plat"`
	Path                string      `toml:"path"`
	AllowEarly          *bool       `toml:"allow_early"`
	SignallerPermission *bool       `toml:"signaller_permission"`
}

type rawRecurrence struct {
	Every             string   `toml:"every"`
	Until             string   `toml:"until"`
	HeadcodeIncrement int      `toml:"headcode_increment"`
	HeadcodeList      []string `toml:"headcode_list"`
}

type rawTemplate struct {
	Template struct {
		Description        string `toml:"description"`
		Operator           string `toml:"operator"`
		ForceTimingProfile string `toml:"force_timing_profile"`
		DoNotAdvertise     *bool  `toml:"do_not_advertise"`
		AllowEarly         *bool  `toml:"allow_early"`
	} `toml:"template"`
	Static     *rawStaticRef `toml:"static"`
	Simulation rawSimulation `toml:"simulation"`
}

type rawSimulation struct {
	Seeds      *rawSeeds  `toml:"seeds"`
	Enters     *rawEnters `toml:"enters"`
	Point      []rawPoint `toml:"point"`
	Exits      *struct{}  `toml:"exits"`
	Terminates *struct{}  `toml:"terminates"`
}

type rawSeeds struct {
	Section   string  `toml:"section"`
	At        float64 `toml:"at"`
	Direction string  `toml:"direction"`
}

type rawEnters struct {
	Section string `toml:"section"`
}

type rawPoint struct {
	Tiploc              string        `toml:"tiploc"`
	TravelTime          string        `toml:"travel_time"`
	Allowance           string        `toml:"allowance"`
	Dwell               string        `toml:"dwell"`
	Pass                bool          `toml:"pass"`
	StopPct             float64       `toml:"stop_pct"`
	Plat                interface{}   `toml:"plat"`
	Path                string        `toml:"path"`
	Activities          []interface{} `toml:"activities"`
	AllowEarly          *bool         `toml:"allow_early"`
	SignallerPermission bool          `toml:"signaller_permission"`
}

type rawConsist struct {
	Description string   `toml:"description"`
	Units       []string `toml:"units"`
	Activities  *struct {
		Reverse *rawActivityRange `toml:"reverse"`
		Attach  *rawActivityRange `toml:"attach"`
		Detach  *rawActivityRange `toml:"detach"`
	} `toml:"activities"`
}

type rawActivityRange struct {
	Min int `toml:"min"`
	Max int `toml:"max"`
}

type rawTimingProfile struct {
	Profile struct {
		Description string   `toml:"description"`
		Units       []string `toml:"units"`
	} `toml:"profile"`
	Segment []rawSegment `toml:"segment"`
}

type rawSegment struct {
	Route string `toml:"route"`
	Stop  string `toml:"stop"`
	Pass  string `toml:"pass"`
}

type rawStationTiming struct {
	TravelTime string `toml:"travel_time"`
	Dwell      string `toml:"dwell"`
}

type rawStaticTemplate struct {
	BeforeSimulatedArea map[string]rawStationTiming `toml:"beforeSimulatedArea"`
	AfterSimulatedArea  map[string]rawStationTiming `toml:"afterSimulatedArea"`
}

type rawTiplocFile struct {
	Name    string `toml:"name"`
	Station *struct {
		CRS string `toml:"crs"`
	} `toml:"station"`
	Custom *struct {
		Section string  `toml:"section"`
		At      float64 `toml:"at"`
		NoRev   bool    `toml:"noRev"`
	} `toml:"custom"`
	Object *struct {
		Object string `toml:"object"`
	} `toml:"object"`
}

type rawStationDef struct {
	Name            string `toml:"name"`
	PlatformLengthM int    `toml:"platform_length_m"`
}

type rawPath struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

type rawConnectionsFile struct {
	Connections map[string][]string `toml:"connections"`
	Unmodelled  []struct {
		At     string  `toml:"at"`
		Weight float64 `toml:"weight"`
	} `toml:"unmodelled"`
}

type staticEntry struct {
	CRS        string
	TravelTime string
	Dwell      string
}

type staticTemplate struct {
	Before []staticEntry
	After  []staticEntry
}

func readTOMLFiles(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]byte{}, nil
		}
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".toml")
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out[id] = data
	}
	return out, nil
}

func splitSectionOffset(s string) (string, *int) {
	if idx := strings.Index(s, ":"); idx >= 0 {
		section := s[:idx]
		if n, err := strconv.Atoi(s[idx+1:]); err == nil {
			return section, &n
		}
		return section, nil
	}
	return s, nil
}

func parseRelDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	suffix := 0
	numPart := s
	switch s[len(s)-1] {
	case 'Q', 'q':
		suffix = 15
		numPart = s[:len(s)-1]
	case 'H', 'h':
		suffix = 30
		numPart = s[:len(s)-1]
	case 'T', 't':
		suffix = 45
		numPart = s[:len(s)-1]
	}
	if numPart == "" {
		numPart = "0"
	}
	mins, err := strconv.Atoi(numPart)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return mins*60 + suffix, nil
}

func parseClockHHMM(s string) (int, error) {
	s = strings.TrimSpace(s)
	suffix := 0
	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'Q', 'q':
			suffix = 15
			s = s[:len(s)-1]
		case 'H', 'h':
			suffix = 30
			s = s[:len(s)-1]
		case 'T', 't':
			suffix = 45
			s = s[:len(s)-1]
		}
	}
	if len(s) != 4 {
		return 0, fmt.Errorf("invalid time %q, expected HHMM", s)
	}
	hh, err := strconv.Atoi(s[0:2])
	if err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	mm, err := strconv.Atoi(s[2:4])
	if err != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return hh*3600 + mm*60 + suffix, nil
}

func formatHHMM(secs int) string {
	secs = ((secs % 86400) + 86400) % 86400
	return fmt.Sprintf("%02d%02d", secs/3600, (secs%3600)/60)
}

func formatClock(secs int) string {
	secs = ((secs % 86400) + 86400) % 86400
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func formatClockPtr(secs *int) string {
	if secs == nil {
		return ""
	}
	return formatClock(*secs)
}

func incrementHeadcode(hc string, n int) string {
	i := len(hc)
	for i > 0 && hc[i-1] >= '0' && hc[i-1] <= '9' {
		i--
	}
	prefix := hc[:i]
	digits := hc[i:]
	if digits == "" {
		return hc
	}
	val, err := strconv.Atoi(digits)
	if err != nil {
		return hc
	}
	val += n
	return fmt.Sprintf("%s%0*d", prefix, len(digits), val)
}

func expandServices(svc rawService) ([]rawService, error) {
	if svc.Recurrence == nil {
		return []rawService{svc}, nil
	}

	refName, refValue, err := recurrenceTimingValue(svc)
	if err != nil {
		return nil, err
	}
	start, err := parseClockHHMM(refValue)
	if err != nil {
		return nil, fmt.Errorf("service %s: %w", svc.Headcode, err)
	}
	everyMin, err := strconv.Atoi(strings.TrimSpace(svc.Recurrence.Every))
	if err != nil {
		return nil, fmt.Errorf("service %s: invalid recurrence.every %q", svc.Headcode, svc.Recurrence.Every)
	}
	until, err := parseClockHHMM(svc.Recurrence.Until)
	if err != nil {
		return nil, fmt.Errorf("service %s: %w", svc.Headcode, err)
	}

	var out []rawService
	idx := 0
	for cur := start; cur <= until; cur += everyMin * 60 {
		clone := svc
		clone.Recurrence = nil
		setRecurrenceTimingValue(&clone, refName, formatHHMM(cur))
		if len(svc.Recurrence.HeadcodeList) > 0 {
			if idx >= len(svc.Recurrence.HeadcodeList) {
				return nil, fmt.Errorf("service %s: headcode_list is shorter than the generated services", svc.Headcode)
			}
			clone.Headcode = svc.Recurrence.HeadcodeList[idx]
		} else {
			clone.Headcode = incrementHeadcode(svc.Headcode, svc.Recurrence.HeadcodeIncrement*idx)
		}
		out = append(out, clone)
		idx++
	}
	if len(svc.Recurrence.HeadcodeList) > 0 && idx != len(svc.Recurrence.HeadcodeList) {
		return nil, fmt.Errorf("service %s: headcode_list has %d entries but %d services were generated",
			svc.Headcode, len(svc.Recurrence.HeadcodeList), idx)
	}
	return out, nil
}

func recurrenceTimingValue(svc rawService) (string, string, error) {
	var refs []struct {
		name  string
		value string
	}
	add := func(name, value string) {
		if value != "" {
			refs = append(refs, struct {
				name  string
				value string
			}{name: name, value: value})
		}
	}
	add("departs", svc.Departs)
	add("enters", svc.Enters)
	add("exits", svc.Exits)
	add("timing", svc.TimingRef)
	for tiploc, value := range svc.TiplocTiming {
		add(tiploc, value)
	}
	if len(refs) == 0 {
		return "", "", fmt.Errorf("service %s: recurrence requires a timing reference", svc.Headcode)
	}
	if len(refs) > 1 {
		return "", "", fmt.Errorf("service %s: recurrence has multiple timing references", svc.Headcode)
	}
	return refs[0].name, refs[0].value, nil
}

func setRecurrenceTimingValue(svc *rawService, name, value string) {
	switch name {
	case "departs":
		svc.Departs = value
	case "enters":
		svc.Enters = value
	case "exits":
		svc.Exits = value
	case "timing":
		svc.TimingRef = value
	default:
		if svc.TiplocTiming == nil {
			svc.TiplocTiming = map[string]string{}
		}
		svc.TiplocTiming[name] = value
	}
}

func gatherDiagramUnits(id string, diagrams map[string]*rawDiagram, consists map[string]*rawConsist, seen map[string]bool, out map[string]bool) error {
	if seen[id] {
		return nil
	}
	seen[id] = true

	d, ok := diagrams[id]
	if !ok {
		return fmt.Errorf("diagram %q not found", id)
	}

	entries := append(append([]allocationEntry{}, d.Diagram.Allocation...), d.Diagram.SetSwapPool...)
	for _, e := range entries {
		if e.Diagram != "" {
			if err := gatherDiagramUnits(e.Diagram, diagrams, consists, seen, out); err != nil {
				return err
			}
			continue
		}
		c, ok := consists[e.Consist]
		if !ok {
			return fmt.Errorf("consist %q not found (referenced by diagram %q)", e.Consist, id)
		}
		for _, u := range c.Units {
			out[u] = true
		}
	}
	return nil
}

func gatherDiagramConsists(id string, diagrams map[string]*rawDiagram, consists map[string]*rawConsist, seen map[string]bool, out map[string]bool) error {
	if seen[id] {
		return nil
	}
	seen[id] = true

	d, ok := diagrams[id]
	if !ok {
		return fmt.Errorf("diagram %q not found", id)
	}

	entries := append(append([]allocationEntry{}, d.Diagram.Allocation...), d.Diagram.SetSwapPool...)
	for _, e := range entries {
		if e.Diagram != "" {
			if err := gatherDiagramConsists(e.Diagram, diagrams, consists, seen, out); err != nil {
				return err
			}
			continue
		}
		if _, ok := consists[e.Consist]; !ok {
			return fmt.Errorf("consist %q not found (referenced by diagram %q)", e.Consist, id)
		}
		out[e.Consist] = true
	}
	return nil
}

func selectTimingProfile(profiles map[string]*rawTimingProfile, profileNames []string, name string, forced string, units []string) (string, *rawTimingProfile, error) {
	pick := name
	if pick == "" {
		pick = forced
	}
	if pick != "" {
		p, ok := profiles[pick]
		if !ok {
			return "", nil, fmt.Errorf("timing profile %q not found", pick)
		}
		return pick, p, nil
	}
	for _, pname := range profileNames {
		p := profiles[pname]
		for _, pu := range p.Profile.Units {
			for _, u := range units {
				if pu == u {
					return pname, p, nil
				}
			}
		}
	}
	return "", nil, fmt.Errorf("no timing profile matches units %v", units)
}

func lookupSegment(profile *rawTimingProfile, from, to string) (*rawSegment, error) {
	want := from + ":" + to
	wantRev := to + ":" + from
	for i := range profile.Segment {
		if profile.Segment[i].Route == want {
			return &profile.Segment[i], nil
		}
	}
	for i := range profile.Segment {
		if profile.Segment[i].Route == wantRev {
			return &profile.Segment[i], nil
		}
	}
	return nil, fmt.Errorf("no timing profile segment found for %s <-> %s", from, to)
}

func travelTimeForLeg(profile *rawTimingProfile, from, to string, stopping bool) (int, error) {
	seg, err := lookupSegment(profile, from, to)
	if err != nil {
		return 0, err
	}
	raw := seg.Pass
	if stopping {
		raw = seg.Stop
	}
	return parseRelDuration(raw)
}

func platToString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func parseActivities(raw []interface{}, svc rawService) []ActivityOut {
	var out []ActivityOut

	for _, item := range raw {
		switch v := item.(type) {
		case string:
			out = append(out, ActivityOut{Type: v})

		case map[string]interface{}:
			a := ActivityOut{}
			if t, ok := v["type"].(string); ok {
				a.Type = t
			}
			out = append(out, a)
		}
	}
	return out
}

func applyServiceActivities(pts []resolvedPoint, activities rawServiceActivities) {
	add := func(activityType string, cfgs []rawActivityConfig) {
		for _, cfg := range cfgs {
			occ := cfg.Occurrence
			if occ == 0 {
				occ = 1
			}
			for i := range pts {
				if pts[i].Kind != PointLocation || pts[i].Tiploc != cfg.Tiploc || pts[i].Occurrence != occ {
					continue
				}
				pts[i].Activities = append(pts[i].Activities, activityFromConfig(activityType, cfg))
				break
			}
		}
	}
	add("attach", activities.Attach)
	add("detach", activities.Detach)
	add("reverse", activities.Reverse)
	add("crew_change", activities.CrewChange)
}

func activityFromConfig(activityType string, cfg rawActivityConfig) ActivityOut {
	a := ActivityOut{
		Type:          activityType,
		Forms:         cfg.Forms,
		TargetDiagram: cfg.DetachDiagram,
		Consists:      cfg.Consists,
	}
	if cfg.Joins != "" {
		a.TargetHeadcode = cfg.Joins
	}
	if cfg.DetachUnit != nil {
		a.TargetUnit = cfg.DetachUnit
	}
	if cfg.AttachUnit != nil {
		a.TargetUnit = cfg.AttachUnit
	}
	return a
}

type resolvedPointKind uint8

const (
	PointLocation resolvedPointKind = iota
	PointPathOnly
)

type resolvedPoint struct {
	Kind resolvedPointKind

	Tiploc              string
	Occurrence          int
	Pass                bool
	Dwell               *int
	TravelTimeOverride  *int
	Allowance           int
	Plat                string
	Path                string
	StopPct             *float64
	AllowEarly          *bool
	SignallerPermission bool
	Activities          []ActivityOut
}

func buildPoints(tmpl *rawTemplate, svc rawService, activityDurations map[string]int) ([]resolvedPoint, error) {
	rawPoints := slices.Clone(tmpl.Simulation.Point)

	pts := make([]resolvedPoint, 0, len(rawPoints))
	occCount := map[string]int{}

	for _, rp := range rawPoints {
		if rp.Tiploc == "" {
			pts = append(pts, resolvedPoint{
				Kind: PointPathOnly,
				Path: rp.Path,
			})
			continue
		}

		p := resolvedPoint{
			Kind:                PointLocation,
			Tiploc:              rp.Tiploc,
			Pass:                rp.Pass,
			Plat:                platToString(rp.Plat),
			Path:                rp.Path,
			Activities:          parseActivities(rp.Activities, svc),
			AllowEarly:          rp.AllowEarly,
			SignallerPermission: rp.SignallerPermission,
		}

		occCount[rp.Tiploc]++
		p.Occurrence = occCount[rp.Tiploc]

		if rp.StopPct != 0 {
			sp := rp.StopPct
			p.StopPct = &sp
		}

		if rp.Dwell != "" {
			d, err := parseRelDuration(rp.Dwell)
			if err != nil {
				return nil, err
			}
			p.Dwell = &d
		}

		if rp.TravelTime != "" {
			t, err := parseRelDuration(rp.TravelTime)
			if err != nil {
				return nil, err
			}
			p.TravelTimeOverride = &t
		}

		if rp.Allowance != "" {
			a, err := parseRelDuration(rp.Allowance)
			if err != nil {
				return nil, err
			}
			p.Allowance = a
		}

		pts = append(pts, p)
	}

	applyServiceActivities(pts, svc.Activity)

	for _, ex := range svc.Exception {
		occ := ex.Occurrence
		if occ == 0 {
			occ = 1
		}

		for i := range pts {
			if pts[i].Kind != PointLocation {
				continue
			}

			if pts[i].Tiploc != ex.Tiploc || pts[i].Occurrence != occ {
				continue
			}

			if ex.TravelTime != "" {
				t, err := parseRelDuration(ex.TravelTime)
				if err != nil {
					return nil, err
				}
				pts[i].TravelTimeOverride = &t
			}

			if ex.Allowance != "" {
				a, err := parseRelDuration(ex.Allowance)
				if err != nil {
					return nil, err
				}
				pts[i].Allowance = a
			}

			if ex.Dwell != "" {
				d, err := parseRelDuration(ex.Dwell)
				if err != nil {
					return nil, err
				}
				pts[i].Dwell = &d
			}

			if ex.Pass != nil {
				pts[i].Pass = *ex.Pass
			}

			if ex.StopPct != nil {
				pts[i].StopPct = ex.StopPct
			}

			if ex.Plat != nil {
				pts[i].Plat = platToString(ex.Plat)
			}

			if ex.Path != "" {
				pts[i].Path = ex.Path
			}

			if ex.AllowEarly != nil {
				pts[i].AllowEarly = ex.AllowEarly
			}

			if ex.SignallerPermission != nil {
				pts[i].SignallerPermission = *ex.SignallerPermission
			}

			break
		}
	}

	for i := range pts {
		if pts[i].Kind != PointLocation || pts[i].Pass {
			continue
		}
		minDwell := 0
		for _, a := range pts[i].Activities {
			if d := activityDurations[a.Type]; d > minDwell {
				minDwell = d
			}
		}
		if minDwell == 0 {
			continue
		}
		if pts[i].Dwell == nil || *pts[i].Dwell < minDwell {
			pts[i].Dwell = &minDwell
		}
	}

	return pts, nil
}

type simCallKind uint8

const (
	SimulatedLocation simCallKind = iota
	SimulatedPathOnly
)

type simCall struct {
	Kind simCallKind

	Path string

	Tiploc              string
	Occurrence          int
	Arr                 *int
	Dep                 *int
	Pass                bool
	Plat                string
	StopPct             *float64
	AllowEarly          *bool
	SignallerPermission bool
	Activities          []ActivityOut
}

func computeSimTimes(profile *rawTimingProfile, entryKey string, pts []resolvedPoint, departs int) ([]simCall, int, error) {
	calls, exitOffset, err := computeSimOffsets(profile, entryKey, pts)
	if err != nil {
		return nil, 0, err
	}
	shiftSimCalls(calls, departs)
	return calls, departs + exitOffset, nil
}

func computeSimOffsets(profile *rawTimingProfile, entryKey string, pts []resolvedPoint) ([]simCall, int, error) {
	if len(pts) == 0 {
		return nil, 0, nil
	}

	t := 0
	var calls []simCall

	first := -1
	for i := range pts {
		if pts[i].Kind == PointLocation {
			first = i
			break
		}
	}

	if first == -1 {
		return calls, 0, nil
	}

	if pts[first].TravelTimeOverride != nil {
		t += *pts[first].TravelTimeOverride
	} else if entryKey != "" {
		travel, err := travelTimeForLeg(profile, entryKey, pts[first].Tiploc, !pts[first].Pass)
		if err != nil {
			return nil, 0, err
		}
		t += travel
	}
	t += pts[first].Allowance

	for i := range pts {
		p := &pts[i]

		if p.Kind == PointPathOnly {
			calls = append(calls, simCall{
				Kind: SimulatedPathOnly,
				Path: p.Path,
			})
			continue
		}

		arr := t
		dep := arr

		if !p.Pass {
			dw := 0
			if p.Dwell != nil {
				dw = *p.Dwell
			}
			dep = arr + dw
		}

		calls = append(calls, simCall{
			Kind:                SimulatedLocation,
			Tiploc:              p.Tiploc,
			Occurrence:          p.Occurrence,
			Arr:                 &arr,
			Dep:                 &dep,
			Pass:                p.Pass,
			Plat:                p.Plat,
			Path:                p.Path,
			StopPct:             p.StopPct,
			AllowEarly:          p.AllowEarly,
			SignallerPermission: p.SignallerPermission,
			Activities:          p.Activities,
		})

		t = dep

		next := -1
		for j := i + 1; j < len(pts); j++ {
			if pts[j].Kind == PointLocation {
				next = j
				break
			}
		}

		if next != -1 {
			if pts[next].TravelTimeOverride != nil {
				t += *pts[next].TravelTimeOverride
			} else {
				travel, err := travelTimeForLeg(profile, p.Tiploc, pts[next].Tiploc, !pts[next].Pass)
				if err != nil {
					return nil, 0, err
				}
				t += travel
			}
			t += pts[next].Allowance
		}
	}

	return calls, t, nil
}

func shiftSimCalls(calls []simCall, delta int) {
	for i := range calls {
		if calls[i].Arr != nil {
			*calls[i].Arr += delta
		}
		if calls[i].Dep != nil {
			*calls[i].Dep += delta
		}
	}
}

func reverseStaticList(l []staticEntry) []staticEntry {
	n := len(l)
	out := make([]staticEntry, n)
	for i := 0; i < n; i++ {
		out[i] = l[n-1-i]
	}
	return out
}

func applyStaticExceptions(list []staticEntry, excs []rawStaticException) []staticEntry {
	if len(excs) == 0 {
		return list
	}
	occCount := map[string]int{}
	occOf := make([]int, len(list))
	for i, e := range list {
		occCount[e.CRS]++
		occOf[i] = occCount[e.CRS]
	}
	excluded := make([]bool, len(list))
	out := make([]staticEntry, len(list))
	copy(out, list)
	for _, ex := range excs {
		occ := ex.Occurrence
		if occ == 0 {
			occ = 1
		}
		for i := range out {
			if out[i].CRS != ex.CRS || occOf[i] != occ {
				continue
			}
			if ex.Exclude {
				excluded[i] = true
			}
			if ex.TravelTime != "" {
				out[i].TravelTime = ex.TravelTime
			}
			if ex.Dwell != "" {
				out[i].Dwell = ex.Dwell
			}
		}
	}
	var final []staticEntry
	for i, e := range out {
		if !excluded[i] {
			final = append(final, e)
		}
	}
	return final
}

type staticCall struct {
	CRS string
	Arr int
	Dep int
}

func computeBeforeTimes(list []staticEntry, departs int) ([]staticCall, error) {
	n := len(list)
	if n == 0 {
		return nil, nil
	}
	arr := make([]int, n)
	dep := make([]int, n)

	prevArr := departs
	for i := n - 1; i >= 0; i-- {
		tt, err := parseRelDuration(list[i].TravelTime)
		if err != nil {
			return nil, err
		}
		dep[i] = prevArr - tt
		d, err := parseRelDuration(list[i].Dwell)
		if err != nil {
			return nil, err
		}
		arr[i] = dep[i] - d
		prevArr = arr[i]
	}

	calls := make([]staticCall, n)
	for i := 0; i < n; i++ {
		calls[i] = staticCall{CRS: list[i].CRS, Arr: arr[i], Dep: dep[i]}
	}
	return calls, nil
}

func computeAfterTimes(list []staticEntry, exitTime int) ([]staticCall, error) {
	var calls []staticCall
	t := exitTime
	for _, e := range list {
		tt, err := parseRelDuration(e.TravelTime)
		if err != nil {
			return nil, err
		}
		arr := t + tt
		d, err := parseRelDuration(e.Dwell)
		if err != nil {
			return nil, err
		}
		dep := arr + d
		calls = append(calls, staticCall{CRS: e.CRS, Arr: arr, Dep: dep})
		t = dep
	}
	return calls, nil
}

func effectiveStatic(svc *rawService, tmpl *rawTemplate) *rawStaticRef {
	if svc.Static != nil {
		return svc.Static
	}
	return tmpl.Static
}

type loadedData struct {
	manifest        *Manifest
	diagrams        map[string]*rawDiagram
	templates       map[string]*rawTemplate
	consists        map[string]*rawConsist
	timingProfiles  map[string]*rawTimingProfile
	profileNames    []string
	staticTemplates map[string]*staticTemplate
	tiplocDefs      map[string]rawTiplocFile
	stationDefs     map[string]rawStationDef
}

type timingReference struct {
	Mode       string
	Tiploc     string
	Occurrence int
	Time       int
}

func resolveTimingReference(svc rawService, manifest *Manifest) (timingReference, error) {
	occ := svc.Occurrence
	if occ == 0 {
		occ = 1
	}
	parse := func(label, value string) (int, error) {
		if value == "" {
			return 0, fmt.Errorf("service %s: missing timing value for %s", svc.Headcode, label)
		}
		t, err := parseClockHHMM(value)
		if err != nil {
			return 0, fmt.Errorf("service %s %s: %w", svc.Headcode, label, err)
		}
		return t, nil
	}

	var refs []timingReference
	if svc.Departs != "" {
		t, err := parse("departs", svc.Departs)
		if err != nil {
			return timingReference{}, err
		}
		refs = append(refs, timingReference{Mode: "enters", Occurrence: occ, Time: t})
	}
	if svc.Enters != "" {
		t, err := parse("enters", svc.Enters)
		if err != nil {
			return timingReference{}, err
		}
		refs = append(refs, timingReference{Mode: "enters", Occurrence: occ, Time: t})
	}
	if svc.Exits != "" {
		t, err := parse("exits", svc.Exits)
		if err != nil {
			return timingReference{}, err
		}
		refs = append(refs, timingReference{Mode: "exits", Occurrence: occ, Time: t})
	}
	if svc.TimingRef != "" {
		t, err := parse("timing", svc.TimingRef)
		if err != nil {
			return timingReference{}, err
		}
		mode := manifest.Timing.TimingMode
		if mode == "" {
			mode = "enters"
		}
		ref := timingReference{Mode: mode, Tiploc: manifest.Timing.Tiploc, Occurrence: manifest.Timing.Occurrence, Time: t}
		if ref.Occurrence == 0 {
			ref.Occurrence = 1
		}
		refs = append(refs, ref)
	}
	for tiploc, value := range svc.TiplocTiming {
		t, err := parse(tiploc, value)
		if err != nil {
			return timingReference{}, err
		}
		refs = append(refs, timingReference{Mode: "tiploc", Tiploc: tiploc, Occurrence: occ, Time: t})
	}
	if len(refs) == 0 {
		return timingReference{}, fmt.Errorf("service %s has no timing reference (departs, enters, exits, timing, or TIPLOC)", svc.Headcode)
	}
	if len(refs) > 1 {
		return timingReference{}, fmt.Errorf("service %s has multiple timing references; use only one of departs, enters, exits, timing, or a TIPLOC", svc.Headcode)
	}
	if refs[0].Mode == "tiploc" && refs[0].Tiploc == "" {
		return timingReference{}, fmt.Errorf("service %s: tiploc timing reference requires a TIPLOC", svc.Headcode)
	}
	return refs[0], nil
}

func entryTimeForReference(ref timingReference, calls []simCall, exitOffset int) (int, error) {
	switch ref.Mode {
	case "", "enters":
		return ref.Time, nil
	case "exits":
		return ref.Time - exitOffset, nil
	case "tiploc":
		for _, c := range calls {
			if c.Kind != SimulatedLocation || c.Tiploc != ref.Tiploc || c.Occurrence != ref.Occurrence {
				continue
			}
			if c.Arr != nil {
				return ref.Time - *c.Arr, nil
			}
			if c.Dep != nil {
				return ref.Time - *c.Dep, nil
			}
			return 0, fmt.Errorf("TIPLOC %s occurrence %d has no time to anchor", ref.Tiploc, ref.Occurrence)
		}
		return 0, fmt.Errorf("TIPLOC %s occurrence %d not found in service", ref.Tiploc, ref.Occurrence)
	default:
		return 0, fmt.Errorf("unsupported timing mode %q", ref.Mode)
	}
}

func buildOutput(dir string, diagramFilter string) (*OutputDoc, error) {
	manifest, err := loadManifest(dir)
	if err != nil {
		return nil, err
	}

	ld := loadedData{manifest: manifest}
	var err2 error
	if ld.diagrams, err2 = loadDiagrams(dir); err2 != nil {
		return nil, err2
	}
	if ld.templates, err2 = loadTemplates(dir); err2 != nil {
		return nil, err2
	}
	if ld.consists, err2 = loadConsists(dir); err2 != nil {
		return nil, err2
	}
	if ld.timingProfiles, err2 = loadTimingProfiles(dir); err2 != nil {
		return nil, err2
	}
	if ld.staticTemplates, err2 = loadStaticTemplates(dir); err2 != nil {
		return nil, err2
	}
	if ld.tiplocDefs, err2 = loadTiplocDefs(dir); err2 != nil {
		return nil, err2
	}
	if ld.stationDefs, err2 = loadStationDefs(dir); err2 != nil {
		return nil, err2
	}
	paths, err2 := loadPaths(dir)
	if err2 != nil {
		return nil, err2
	}
	connections, fringe, err2 := loadConnections(dir)
	if err2 != nil {
		return nil, err2
	}

	for name := range ld.timingProfiles {
		ld.profileNames = append(ld.profileNames, name)
	}
	sort.Strings(ld.profileNames)

	doc := &OutputDoc{
		Manifest:      manifest,
		Tiplocs:       buildTiplocsOut(ld.tiplocDefs),
		Paths:         paths,
		Consists:      buildConsistsOut(ld.consists),
		Connections:   connections,
		FringeWeights: fringe,
		Stations:      buildStationsOut(ld.stationDefs),
	}

	var diagramIDs []string
	for id := range ld.diagrams {
		if diagramFilter != "" && id != diagramFilter {
			continue
		}
		diagramIDs = append(diagramIDs, id)
	}
	sort.Strings(diagramIDs)
	if diagramFilter != "" && len(diagramIDs) == 0 {
		return nil, fmt.Errorf("diagram %q not found", diagramFilter)
	}

	for _, diagID := range diagramIDs {
		out, err := buildDiagramOut(diagID, ld)
		if err != nil {
			return nil, fmt.Errorf("diagram %s: %w", diagID, err)
		}
		doc.Diagrams = append(doc.Diagrams, out)
	}

	return doc, nil
}

func buildTiplocsOut(defs map[string]rawTiplocFile) []TiplocOut {
	var ids []string
	for id := range defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []TiplocOut
	for _, id := range ids {
		t := defs[id]
		o := TiplocOut{ID: id, Name: t.Name}
		switch {
		case t.Station != nil:
			o.Type = "station"
			o.CRS = t.Station.CRS
		case t.Custom != nil:
			o.Type = "custom"
			o.Section = t.Custom.Section
			o.At = t.Custom.At
			o.NoRev = t.Custom.NoRev
		case t.Object != nil:
			o.Type = "object"
			o.Object = t.Object.Object
		}
		out = append(out, o)
	}
	return out
}

func buildConsistsOut(consists map[string]*rawConsist) []ConsistOut {
	var ids []string
	for id := range consists {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []ConsistOut
	for _, id := range ids {
		c := consists[id]
		co := ConsistOut{ID: id, Description: c.Description, Units: c.Units}
		if c.Activities != nil {
			acts := map[string]ActivityRangeOut{}
			if c.Activities.Reverse != nil {
				acts["reverse"] = ActivityRangeOut{Min: c.Activities.Reverse.Min, Max: c.Activities.Reverse.Max}
			}
			if c.Activities.Attach != nil {
				acts["attach"] = ActivityRangeOut{Min: c.Activities.Attach.Min, Max: c.Activities.Attach.Max}
			}
			if c.Activities.Detach != nil {
				acts["detach"] = ActivityRangeOut{Min: c.Activities.Detach.Min, Max: c.Activities.Detach.Max}
			}
			if len(acts) > 0 {
				co.Activities = acts
			}
		}
		out = append(out, co)
	}
	return out
}

func buildStationsOut(defs map[string]rawStationDef) []StationOut {
	var crss []string
	for crs := range defs {
		crss = append(crss, crs)
	}
	sort.Strings(crss)

	var out []StationOut
	for _, crs := range crss {
		d := defs[crs]
		out = append(out, StationOut{CRS: crs, Name: d.Name, PlatLength: d.PlatformLengthM})
	}
	return out
}

func activityDurationsForConsists(consistIDs []string, consists map[string]*rawConsist) map[string]int {
	out := map[string]int{}
	add := func(name string, r *rawActivityRange) {
		if r == nil {
			return
		}
		if r.Max > out[name] {
			out[name] = r.Max
		}
	}
	for _, id := range consistIDs {
		c := consists[id]
		if c == nil || c.Activities == nil {
			continue
		}
		add("reverse", c.Activities.Reverse)
		add("attach", c.Activities.Attach)
		add("detach", c.Activities.Detach)
	}
	return out
}

func resolveBool(service, diagram, template *bool) *bool {
	switch {
	case service != nil:
		return service
	case diagram != nil:
		return diagram
	case template != nil:
		return template
	default:
		return nil
	}
}

func boolValueFromPtr(v *bool) bool {
	return v != nil && *v
}

func scenarioOutFromRaw(s *rawScenario) *ScenarioOut {
	if s == nil {
		return nil
	}
	return &ScenarioOut{
		BaseDelay:      s.BaseDelay,
		DelayedPct:     s.DelayedPct,
		DisruptionPct:  s.DisruptionPct,
		SetSwapPct:     s.SetSwapPct,
		RunsAsRequired: s.RunsAsRequired,
	}
}

func serviceScenarioOut(diagram *rawScenario, svc rawService) *ScenarioOut {
	hasOverride := svc.BaseDelay != nil || svc.DelayedPct != nil || svc.DisruptionPct != nil ||
		svc.SetSwapPct != nil || svc.RunsAsRequired != nil
	if !hasOverride {
		return nil
	}
	out := ScenarioOut{}
	if diagram != nil {
		out = *scenarioOutFromRaw(diagram)
	}
	if svc.BaseDelay != nil {
		out.BaseDelay = *svc.BaseDelay
	}
	if svc.DelayedPct != nil {
		out.DelayedPct = *svc.DelayedPct
	}
	if svc.DisruptionPct != nil {
		out.DisruptionPct = *svc.DisruptionPct
	}
	if svc.SetSwapPct != nil {
		out.SetSwapPct = *svc.SetSwapPct
	}
	if svc.RunsAsRequired != nil {
		out.RunsAsRequired = svc.RunsAsRequired
	}
	return &out
}

func buildDiagramOut(diagID string, ld loadedData) (DiagramOut, error) {
	d := ld.diagrams[diagID]

	out := DiagramOut{ID: diagID}

	out.Operator = d.Diagram.Operator
	out.DoNotAdvertise = boolValueFromPtr(d.Diagram.DoNotAdvertise)
	out.AllowEarly = d.Diagram.AllowEarly

	for _, e := range d.Diagram.Allocation {
		out.Allocation = append(out.Allocation, AllocEntryOut{Consist: e.Consist, Diagram: e.Diagram, Weight: e.Weight})
	}
	for _, e := range d.Diagram.SetSwapPool {
		out.SetSwapPool = append(out.SetSwapPool, AllocEntryOut{Consist: e.Consist, Diagram: e.Diagram, Weight: e.Weight})
	}
	out.Scenario = scenarioOutFromRaw(d.Scenario)

	unitSet := map[string]bool{}
	if err := gatherDiagramUnits(diagID, ld.diagrams, ld.consists, map[string]bool{}, unitSet); err != nil {
		return out, err
	}
	var allUnits []string
	for u := range unitSet {
		allUnits = append(allUnits, u)
	}
	sort.Strings(allUnits)

	consistSet := map[string]bool{}
	if err := gatherDiagramConsists(diagID, ld.diagrams, ld.consists, map[string]bool{}, consistSet); err != nil {
		return out, err
	}
	var allConsists []string
	for c := range consistSet {
		allConsists = append(allConsists, c)
	}
	sort.Strings(allConsists)

	for _, baseSvc := range d.Service {
		services, err := expandServices(baseSvc)
		if err != nil {
			return out, err
		}
		for _, svc := range services {
			svcOut, err := convertService(diagID, svc, allUnits, allConsists, d, ld)
			if err != nil {
				return out, fmt.Errorf("service %s: %w", svc.Headcode, err)
			}
			out.Services = append(out.Services, svcOut)
		}
	}

	return out, nil
}

// readTOMLFilesRecursive walks a directory tree and loads all .toml files.
// It uses just the clean filename (without subfolders) as the map key ID.
func readTOMLFilesRecursive(baseDir string) (map[string][]byte, error) {
	out := make(map[string][]byte)

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if strings.ToLower(filepath.Ext(path)) != ".toml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// CHANGE: Get just the filename (e.g., "BranchKestby.toml") instead of the full relative path
		filename := filepath.Base(path)

		// Strip the ".toml" extension to get the standard ID (e.g., "BranchKestby")
		id := strings.TrimSuffix(filename, filepath.Ext(filename))

		out[id] = data
		return nil
	})

	if err != nil {
		return nil, err
	}
	return out, nil
}

// ============================================================================
// 2. Updated Directory Loader Functions
// ============================================================================

func loadDiagrams(dir string) (map[string]*rawDiagram, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "Diagrams"))
	if err != nil {
		return nil, err
	}
	out := map[string]*rawDiagram{}
	for id, data := range files {
		var multi struct {
			Diagram []rawDiagram `toml:"diagram"`
		}
		if err := toml.Unmarshal(data, &multi); err == nil && len(multi.Diagram) > 0 {
			if len(multi.Diagram) == 1 {
				out[id] = &multi.Diagram[0]
			} else {
				for i := range multi.Diagram {
					subid := id
					if i > 0 {
						subid = fmt.Sprintf("%s#%d", id, i)
					}
					out[subid] = &multi.Diagram[i]
				}
			}
			continue
		}

		var single rawDiagram
		if err := toml.Unmarshal(data, &single); err == nil {
			if len(single.Service) > 0 || single.Scenario != nil ||
				len(single.Diagram.Allocation) > 0 || len(single.Diagram.SetSwapPool) > 0 {
				out[id] = &single
				continue
			}
		}

		var wrap struct {
			Diagram rawDiagram `toml:"diagram"`
		}
		if err := toml.Unmarshal(data, &wrap); err == nil {
			out[id] = &wrap.Diagram
			continue
		}

		return nil, fmt.Errorf("parse Diagrams/%s.toml: could not decode diagram file", id)
	}
	return out, nil
}

func loadTemplates(dir string) (map[string]*rawTemplate, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "Templates"))
	if err != nil {
		return nil, err
	}
	out := map[string]*rawTemplate{}
	for id, data := range files {
		var t rawTemplate
		if err := toml.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("parse Templates/%s.toml: %w", id, err)
		}
		out[id] = &t
	}
	return out, nil
}

func loadConsists(dir string) (map[string]*rawConsist, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "Consists"))
	if err != nil {
		return nil, err
	}
	out := map[string]*rawConsist{}
	for id, data := range files {
		var c rawConsist
		if err := toml.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("parse Consists/%s.toml: %w", id, err)
		}
		out[id] = &c
	}
	return out, nil
}

func loadTimingProfiles(dir string) (map[string]*rawTimingProfile, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "TimingProfiles"))
	if err != nil {
		return nil, err
	}
	out := map[string]*rawTimingProfile{}
	for id, data := range files {
		var p rawTimingProfile
		if err := toml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parse TimingProfiles/%s.toml: %w", id, err)
		}
		out[id] = &p
	}
	return out, nil
}

func loadTiplocDefs(dir string) (map[string]rawTiplocFile, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "TIPLOCs"))
	if err != nil {
		return nil, err
	}
	out := map[string]rawTiplocFile{}
	for id, data := range files {
		var t rawTiplocFile
		if err := toml.Unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("parse TIPLOCs/%s.toml: %w", id, err)
		}
		out[id] = t
	}
	return out, nil
}

func loadStationDefs(dir string) (map[string]rawStationDef, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "Static", "Definitions"))
	if err != nil {
		return nil, err
	}
	out := map[string]rawStationDef{}
	for crs, data := range files {
		var s rawStationDef
		if err := toml.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("parse Static/Definitions/%s.toml: %w", crs, err)
		}
		out[crs] = s
	}
	return out, nil
}

func loadPaths(dir string) ([]PathOut, error) {
	files, err := readTOMLFilesRecursive(filepath.Join(dir, "Paths"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for id := range files {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []PathOut
	for _, id := range ids {
		var p rawPath
		if err := toml.Unmarshal(files[id], &p); err != nil {
			return nil, fmt.Errorf("parse Paths/%s.toml: %w", id, err)
		}
		fromSection, fromAt := splitSectionOffset(p.From)
		toSection, toAt := splitSectionOffset(p.To)
		out = append(out, PathOut{
			ID: id, FromSection: fromSection, ToSection: toSection,
			FromAt: fromAt, ToAt: toAt,
		})
	}
	return out, nil
}

func loadStaticTemplates(dir string) (map[string]*staticTemplate, error) {
	root := filepath.Join(dir, "Static", "StaticTemplates")
	files, err := readTOMLFilesRecursive(root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*staticTemplate{}, nil
		}
		return nil, err
	}

	out := map[string]*staticTemplate{}
	for id, data := range files {
		var raw rawStaticTemplate
		md, err := toml.Decode(string(data), &raw)
		if err != nil {
			return nil, fmt.Errorf("parse Static/StaticTemplates/%s.toml: %w", id, err)
		}

		st := &staticTemplate{}
		seenBefore := map[string]bool{}
		seenAfter := map[string]bool{}
		for _, k := range md.Keys() {
			if len(k) < 2 {
				continue
			}
			switch k[0] {
			case "beforeSimulatedArea":
				crs := k[1]
				if !seenBefore[crs] {
					seenBefore[crs] = true
					v := raw.BeforeSimulatedArea[crs]
					st.Before = append(st.Before, staticEntry{CRS: crs, TravelTime: v.TravelTime, Dwell: v.Dwell})
				}
			case "afterSimulatedArea":
				crs := k[1]
				if !seenAfter[crs] {
					seenAfter[crs] = true
					v := raw.AfterSimulatedArea[crs]
					st.After = append(st.After, staticEntry{CRS: crs, TravelTime: v.TravelTime, Dwell: v.Dwell})
				}
			}
		}
		out[id] = st
	}
	return out, nil
}

func loadConnections(dir string) (map[string][]string, map[string]float64, error) {
	path := filepath.Join(dir, "Static", "Connections.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]string{}, map[string]float64{}, nil
		}
		return nil, nil, fmt.Errorf("read Static/Connections.toml: %w", err)
	}
	var raw rawConnectionsFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("parse Static/Connections.toml: %w", err)
	}
	fringe := map[string]float64{}
	for _, u := range raw.Unmodelled {
		fringe[u.At] = u.Weight
	}
	if raw.Connections == nil {
		raw.Connections = map[string][]string{}
	}
	return raw.Connections, fringe, nil
}

func convertService(diagID string, svc rawService, allUnits []string, allConsists []string, diagram *rawDiagram, ld loadedData) (ServiceOut, error) {
	tmpl, ok := ld.templates[svc.Template]
	if !ok {
		return ServiceOut{}, fmt.Errorf("template %q not found", svc.Template)
	}

	ref, err := resolveTimingReference(svc, ld.manifest)
	if err != nil {
		return ServiceOut{}, err
	}

	forcedProfile := tmpl.Template.ForceTimingProfile
	requestedProfile := ""
	if svc.Timing != nil {
		requestedProfile = svc.Timing.Profile
	}
	profileName, profile, err := selectTimingProfile(ld.timingProfiles, ld.profileNames, requestedProfile, forcedProfile, allUnits)
	if err != nil {
		return ServiceOut{}, err
	}

	pts, err := buildPoints(tmpl, svc, activityDurationsForConsists(allConsists, ld.consists))
	if err != nil {
		return ServiceOut{}, err
	}

	entryKey := ""
	entry := EntryOut{}
	switch {
	case tmpl.Simulation.Seeds != nil:
		entry = EntryOut{Type: "seeds", Section: tmpl.Simulation.Seeds.Section, At: tmpl.Simulation.Seeds.At, Direction: tmpl.Simulation.Seeds.Direction}
	case tmpl.Simulation.Enters != nil:
		entry = EntryOut{Type: "enters", Section: tmpl.Simulation.Enters.Section}
		entryKey = tmpl.Simulation.Enters.Section
	default:
		entry = EntryOut{Type: "forms"}
	}

	exit := ExitOut{}
	switch {
	case tmpl.Simulation.Exits != nil:
		exit = ExitOut{Type: "exits"}
	case tmpl.Simulation.Terminates != nil:
		exit = ExitOut{Type: "terminates"}
	default:
		return ServiceOut{}, fmt.Errorf("template %q has neither simulation.exits nor simulation.terminates", svc.Template)
	}

	var before []staticCall
	var after []staticCall

	staticRef := effectiveStatic(&svc, tmpl)
	var beforeList []staticEntry
	var afterList []staticEntry
	var simCalls []simCall
	var exitTime int

	offsetCalls, exitOffset, err := computeSimOffsets(profile, entryKey, pts)
	if err != nil {
		return ServiceOut{}, err
	}
	departs, err := entryTimeForReference(ref, offsetCalls, exitOffset)
	if err != nil {
		return ServiceOut{}, err
	}

	if staticRef != nil {
		st, ok := ld.staticTemplates[staticRef.Template]
		if !ok {
			return ServiceOut{}, fmt.Errorf("static template %q not found", staticRef.Template)
		}

		beforeList = slices.Clone(st.Before)
		afterList = slices.Clone(st.After)

		if staticRef.Reversed {
			reversedBefore := reverseStaticList(beforeList)
			reversedAfter := reverseStaticList(afterList)
			beforeList = reversedAfter
			afterList = reversedBefore
		}

		beforeList = applyStaticExceptions(beforeList, staticRef.Exception)
		afterList = applyStaticExceptions(afterList, staticRef.Exception)

		before, err = computeBeforeTimes(beforeList, departs)
		if err != nil {
			return ServiceOut{}, err
		}

		after, err = computeAfterTimes(afterList, 0)
		if err != nil {
			return ServiceOut{}, err
		}
	}

	simCalls = offsetCalls
	shiftSimCalls(simCalls, departs)
	exitTime = departs + exitOffset

	if staticRef != nil {
		after, err = computeAfterTimes(afterList, exitTime)
		if err != nil {
			return ServiceOut{}, err
		}
	}

	firstSim := -1
	lastSim := -1
	for i, c := range simCalls {
		if c.Kind != SimulatedLocation {
			continue
		}
		if firstSim == -1 {
			firstSim = i
		}
		lastSim = i
	}

	if firstSim != -1 && firstSim == 0 && len(before) == 0 {
		simCalls[firstSim].Arr = nil
	}
	if lastSim != -1 && lastSim == len(simCalls)-1 && len(after) == 0 {
		simCalls[lastSim].Dep = nil
	}

	svcOut := ServiceOut{
		Headcode:       svc.Headcode,
		Diagram:        diagID,
		EntryTime:      formatClock(departs),
		TimingLoad:     profileName,
		DoNotAdvertise: boolValueFromPtr(resolveBool(svc.DoNotAdvertise, diagram.Diagram.DoNotAdvertise, tmpl.Template.DoNotAdvertise)),
		AllowEarly:     resolveBool(svc.AllowEarly, diagram.Diagram.AllowEarly, tmpl.Template.AllowEarly),
		Scenario:       serviceScenarioOut(diagram.Scenario, svc),
		Entry:          entry,
		Exit:           exit,
	}

	for _, c := range before {
		svcOut.Timetable = append(svcOut.Timetable, TimetableEntryOut{
			Type: "unsimulated",
			CRS:  c.CRS,
			Arr:  formatClock(c.Arr),
			Dep:  formatClock(c.Dep),
		})
	}

	for _, c := range simCalls {
		switch c.Kind {
		case SimulatedPathOnly:
			svcOut.Timetable = append(svcOut.Timetable, TimetableEntryOut{
				Type: "simulatedPathOnly",
				Path: c.Path,
			})

		case SimulatedLocation:
			svcOut.Timetable = append(svcOut.Timetable, TimetableEntryOut{
				Type:                "simulated",
				Tiploc:              c.Tiploc,
				Arr:                 formatClockPtr(c.Arr),
				Dep:                 formatClockPtr(c.Dep),
				Plat:                c.Plat,
				Pass:                c.Pass,
				Path:                c.Path,
				StopPct:             c.StopPct,
				AllowEarly:          c.AllowEarly,
				SignallerPermission: c.SignallerPermission,
				Activities:          c.Activities,
			})
		}
	}

	for _, c := range after {
		svcOut.Timetable = append(svcOut.Timetable, TimetableEntryOut{
			Type: "unsimulated",
			CRS:  c.CRS,
			Arr:  formatClock(c.Arr),
			Dep:  formatClock(c.Dep),
		})
	}

	if len(svcOut.Timetable) > 0 {
		svcOut.Timetable[0].Arr = ""
		svcOut.Timetable[len(svcOut.Timetable)-1].Dep = ""
	}

	return svcOut, nil
}
