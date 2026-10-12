package controller

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"cdpsh/internal/protocol"
	"github.com/chromedp/cdproto/cdp"
)

// DOM supplies ancestry and shadow boundaries; AX supplies semantics; snapshots
// supply rendered text and layout. None of the raw trees are returned to callers.
type domNode struct {
	Backend      cdp.BackendNodeID `json:"backendNodeId"`
	Tag          string            `json:"localName"`
	Type         int               `json:"nodeType"`
	Value        string            `json:"nodeValue"`
	Attributes   []string          `json:"attributes"`
	Children     []*domNode        `json:"children"`
	Shadows      []*domNode        `json:"shadowRoots"`
	Shadow       string            `json:"shadowRootType"`
	Scrollable   bool              `json:"isScrollable"`
	AssignedSlot *struct {
		Backend cdp.BackendNodeID `json:"backendNodeId"`
	} `json:"assignedSlot"`
}

func (n *domNode) attr(name string) string {
	for i := 0; i+1 < len(n.Attributes); i += 2 {
		if n.Attributes[i] == name {
			return n.Attributes[i+1]
		}
	}
	return ""
}

type group struct {
	Role        string `json:"role"`
	Name        string `json:"name,omitempty"`
	ShadowDepth int    `json:"shadowDepth,omitempty"`
}
type shadowBoundary struct {
	Host string `json:"host"`
	Mode string `json:"mode"`
}
type viewportInfo struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Above  float64 `json:"above"`
	Below  float64 `json:"below"`
}
type elementState struct {
	Checked          any            `json:"checked,omitempty"`
	Selected         *bool          `json:"selected,omitempty"`
	Expanded         *bool          `json:"expanded,omitempty"`
	Pressed          any            `json:"pressed,omitempty"`
	Focused          bool           `json:"focused,omitempty"`
	Readonly         bool           `json:"readonly,omitempty"`
	Required         bool           `json:"required,omitempty"`
	Value            *string        `json:"value,omitempty"`
	Placeholder      string         `json:"placeholder,omitempty"`
	InputType        string         `json:"inputType,omitempty"`
	Scrollable       bool           `json:"scrollable,omitempty"`
	ScrollAbove      float64        `json:"scrollAbove,omitempty"`
	ScrollBelow      float64        `json:"scrollBelow,omitempty"`
	ValueTruncated   bool           `json:"valueTruncated,omitempty"`
	Options          []selectOption `json:"options,omitempty"`
	OptionCount      int            `json:"optionCount,omitempty"`
	OptionsTruncated bool           `json:"optionsTruncated,omitempty"`
}

type selectOption struct {
	Index         int    `json:"index"`
	Value         string `json:"value"`
	Label         string `json:"label"`
	Disabled      bool   `json:"disabled,omitempty"`
	Selected      bool   `json:"selected"`
	Truncated     bool   `json:"truncated,omitempty"`
	ValueRedacted bool   `json:"valueRedacted,omitempty"`
}

type perception struct {
	nodes    map[cdp.BackendNodeID]*domNode
	parents  map[cdp.BackendNodeID]*domNode
	ax       map[cdp.BackendNodeID]axNode
	order    []axNode
	domOrder []*domNode
	text     string
	viewport viewportInfo
}

func (t *tab) perceive(ctx context.Context, scope string) (*perception, error) {
	p := &perception{nodes: map[cdp.BackendNodeID]*domNode{}, parents: map[cdp.BackendNodeID]*domNode{}, ax: map[cdp.BackendNodeID]axNode{}}
	var tree struct {
		Root *domNode `json:"root"`
	}
	if err := t.call(ctx, "DOM.getDocument", map[string]any{"depth": -1, "pierce": true}, &tree); err != nil {
		return nil, err
	}
	var walk func(*domNode, *domNode)
	walk = func(n, parent *domNode) {
		if n == nil {
			return
		}
		p.nodes[n.Backend], p.parents[n.Backend] = n, parent
		p.domOrder = append(p.domOrder, n)
		for _, child := range n.Children {
			walk(child, n)
		}
		for _, child := range n.Shadows {
			walk(child, n)
		}
	}
	walk(tree.Root, nil)
	for _, n := range p.domOrder {
		if n.AssignedSlot != nil && p.nodes[n.AssignedSlot.Backend] != nil {
			p.parents[n.Backend] = p.nodes[n.AssignedSlot.Backend]
		}
	}
	nodes, err := t.ax(ctx)
	if err != nil {
		return nil, err
	}
	p.order = nodes
	for _, n := range nodes {
		if !n.Ignored {
			p.ax[n.BackendDOMNodeID] = n
		}
	}
	v, err := t.evaluate(ctx, `({width:innerWidth,height:innerHeight,x:scrollX,y:scrollY,above:Math.max(0,scrollY),below:Math.max(0,(document.scrollingElement?.scrollHeight||innerHeight)-innerHeight-scrollY)})`)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(v, &p.viewport); err != nil {
		return nil, err
	}
	var snapshot struct {
		Strings   []string `json:"strings"`
		Documents []struct {
			Nodes struct {
				Backends  []cdp.BackendNodeID `json:"backendNodeId"`
				Clickable struct {
					Index []int `json:"index"`
				} `json:"isClickable"`
			} `json:"nodes"`
			Layout struct {
				Indices []int       `json:"nodeIndex"`
				Text    []int       `json:"text"`
				Bounds  [][]float64 `json:"bounds"`
				Styles  [][]int     `json:"styles"`
			} `json:"layout"`
			TextBoxes struct {
				Layout []int       `json:"layoutIndex"`
				Start  []int       `json:"start"`
				Length []int       `json:"length"`
				Bounds [][]float64 `json:"bounds"`
			} `json:"textBoxes"`
		} `json:"documents"`
	}
	if err = t.call(ctx, "DOMSnapshot.captureSnapshot", map[string]any{"computedStyles": []string{"visibility", "opacity", "overflow-x", "overflow-y"}}, &snapshot); err != nil {
		return nil, err
	}
	var text strings.Builder
	// The first snapshot document is the main frame. Child frames are reported
	// separately, never assigned misleading main-frame references.
	if len(snapshot.Documents) > 0 {
		d := snapshot.Documents[0]
		boxes := map[int][]int{}
		for i, index := range d.TextBoxes.Layout {
			boxes[index] = append(boxes[index], i)
		}
		layoutIndex := map[cdp.BackendNodeID]int{}
		for i, idx := range d.Layout.Indices {
			if idx >= 0 && idx < len(d.Nodes.Backends) {
				layoutIndex[d.Nodes.Backends[idx]] = i
			}
		}
		style := func(i, property int) string {
			if i < 0 || i >= len(d.Layout.Styles) || property >= len(d.Layout.Styles[i]) {
				return ""
			}
			idx := d.Layout.Styles[i][property]
			if idx < 0 || idx >= len(snapshot.Strings) {
				return ""
			}
			return snapshot.Strings[idx]
		}
		visibleBox := func(backend cdp.BackendNodeID, b []float64) bool {
			if len(b) < 4 {
				return false
			}
			left, right, top, bottom := b[0], b[0]+b[2], b[1], b[1]+b[3]
			if scope == "viewport" {
				left = max(left, p.viewport.X)
				right = min(right, p.viewport.X+p.viewport.Width)
				top = max(top, p.viewport.Y)
				bottom = min(bottom, p.viewport.Y+p.viewport.Height)
			}
			for n := p.parents[backend]; n != nil; n = p.parents[n.Backend] {
				idx, ok := layoutIndex[n.Backend]
				if !ok {
					continue
				}
				if style(idx, 1) == "0" {
					return false
				}
				if idx >= len(d.Layout.Bounds) {
					continue
				}
				r := d.Layout.Bounds[idx]
				if len(r) < 4 {
					continue
				}
				if strings.Contains(" auto scroll hidden clip ", " "+style(idx, 2)+" ") && style(idx, 2) != "" {
					left = max(left, r[0])
					right = min(right, r[0]+r[2])
				}
				if strings.Contains(" auto scroll hidden clip ", " "+style(idx, 3)+" ") && style(idx, 3) != "" {
					top = max(top, r[1])
					bottom = min(bottom, r[1]+r[3])
				}
			}
			return right > left && bottom > top
		}
		clickable := map[cdp.BackendNodeID]bool{}
		for _, idx := range d.Nodes.Clickable.Index {
			if idx >= 0 && idx < len(d.Nodes.Backends) {
				clickable[d.Nodes.Backends[idx]] = true
			}
		}
		for _, n := range p.domOrder {
			if n.Type != 1 || n.Tag == "body" || n.Tag == "html" || interactive(p.ax[n.Backend]) || (!clickable[n.Backend] && !n.Scrollable) {
				continue
			}
			ua := false
			for ancestor := n; ancestor != nil; ancestor = p.parents[ancestor.Backend] {
				if ancestor.Shadow == "user-agent" {
					ua = true
					break
				}
			}
			if ua {
				continue
			}
			role := "button"
			if !clickable[n.Backend] {
				role = "scrollbar"
			}
			name := n.attr("aria-label")
			if name == "" {
				name = nodeText(n, 200)
			}
			rb, _ := json.Marshal(role)
			nb, _ := json.Marshal(name)
			p.order = append(p.order, axNode{Inferred: true, BackendDOMNodeID: n.Backend, Role: &axValue{Value: rb}, Name: &axValue{Value: nb}})
		}
		for i, idx := range d.Layout.Indices {
			if i >= len(d.Layout.Text) || i >= len(d.Layout.Bounds) || idx < 0 || idx >= len(d.Nodes.Backends) {
				continue
			}
			str := d.Layout.Text[i]
			if str < 0 || str >= len(snapshot.Strings) {
				continue
			}
			s := strings.TrimSpace(snapshot.Strings[str])
			b := d.Layout.Bounds[i]
			if s == "" || len(b) < 4 || b[2] <= 0 || b[3] <= 0 {
				continue
			}
			if i < len(d.Layout.Styles) {
				styles := d.Layout.Styles[i]
				if len(styles) > 0 && styles[0] >= 0 && styles[0] < len(snapshot.Strings) && snapshot.Strings[styles[0]] != "visible" {
					continue
				}
				if len(styles) > 1 && styles[1] >= 0 && styles[1] < len(snapshot.Strings) && snapshot.Strings[styles[1]] == "0" {
					continue
				}
			}
			if !visibleBox(d.Nodes.Backends[idx], b) {
				continue
			}
			// Never include text from browser-owned input internals or sensitive controls.
			sensitive := false
			for n := p.nodes[d.Nodes.Backends[idx]]; n != nil; n = p.parents[n.Backend] {
				if n.Shadow == "user-agent" || sensitiveInput(n) {
					sensitive = true
					break
				}
			}
			if !sensitive {
				if scope == "viewport" && len(boxes[i]) > 0 {
					units := utf16.Encode([]rune(snapshot.Strings[str]))
					for _, box := range boxes[i] {
						if box >= len(d.TextBoxes.Start) || box >= len(d.TextBoxes.Length) || box >= len(d.TextBoxes.Bounds) || !visibleBox(d.Nodes.Backends[idx], d.TextBoxes.Bounds[box]) {
							continue
						}
						start, end := d.TextBoxes.Start[box], d.TextBoxes.Start[box]+d.TextBoxes.Length[box]
						if start < 0 || end > len(units) {
							continue
						}
						piece := strings.TrimSpace(string(utf16.Decode(units[start:end])))
						if piece != "" {
							text.WriteString(piece)
							text.WriteByte('\n')
						}
					}
				} else {
					text.WriteString(s)
					text.WriteByte('\n')
				}
			}
		}
	}
	p.text = strings.TrimSpace(text.String())
	return p, nil
}

func sensitiveInput(n *domNode) bool {
	typ, autocomplete := strings.ToLower(n.attr("type")), strings.ToLower(n.attr("autocomplete"))
	return typ == "password" || typ == "file" || typ == "hidden" || strings.Contains(autocomplete, "cc-") || strings.Contains(autocomplete, "one-time-code")
}

func (p *perception) ancestry(backend cdp.BackendNodeID) ([]group, []shadowBoundary) {
	var groups []group
	var shadows []shadowBoundary
	for n := p.parents[backend]; n != nil; n = p.parents[n.Backend] {
		if n.Shadow != "" {
			host := p.parents[n.Backend]
			if host != nil {
				label := host.Tag
				if id := host.attr("id"); id != "" {
					label += "#" + id
				}
				shadows = append(shadows, shadowBoundary{clip(label, 80), n.Shadow})
			}
		}
		ax := p.ax[n.Backend]
		role, name := axString(ax.Role), axString(ax.Name)
		if role == "" || role == "generic" {
			role = n.Tag
		}
		switch role {
		case "form", "dialog", "alertdialog", "navigation", "main", "region", "section", "article", "row", "listitem", "group", "fieldset":
			if name == "" {
				name = n.attr("aria-label")
			}
			if name == "" {
				name = heading(n)
			}
			if name == "" && (role == "row" || role == "listitem") {
				name = nodeText(n, 120)
			}
			if name != "" || role == "dialog" || role == "form" || role == "main" {
				depth := 0
				for ancestor := p.parents[n.Backend]; ancestor != nil; ancestor = p.parents[ancestor.Backend] {
					if ancestor.Shadow != "" {
						depth++
					}
				}
				groups = append(groups, group{Role: role, Name: clip(name, 120), ShadowDepth: depth})
			}
		}
	}
	for i, j := 0, len(groups)-1; i < j; i, j = i+1, j-1 {
		groups[i], groups[j] = groups[j], groups[i]
	}
	if len(groups) > 4 {
		groups = groups[len(groups)-4:]
	}
	for i, j := 0, len(shadows)-1; i < j; i, j = i+1, j-1 {
		shadows[i], shadows[j] = shadows[j], shadows[i]
	}
	return groups, shadows
}

func nodeText(n *domNode, max int) string {
	var b strings.Builder
	var walk func(*domNode)
	walk = func(n *domNode) {
		if b.Len() >= max || sensitiveInput(n) || n.Tag == "script" || n.Tag == "style" {
			return
		}
		if n.Type == 3 {
			b.WriteString(n.Value)
			b.WriteByte(' ')
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(n)
	return clip(strings.Join(strings.Fields(b.String()), " "), max)
}

func heading(n *domNode) string {
	for _, child := range n.Children {
		if child.Tag == "legend" || child.Tag == "caption" || (len(child.Tag) == 2 && child.Tag[0] == 'h' && child.Tag[1] >= '1' && child.Tag[1] <= '6') {
			return nodeText(child, 120)
		}
	}
	return ""
}

// Byte offsets refer to the immutable, rendered text stored with an observation.
func textPage(s string, offset, max int) (string, int, error) {
	if offset < 0 || offset > len(s) || (offset < len(s) && !utf8.RuneStart(s[offset])) {
		return "", 0, protocol.Fail("INVALID_ARGUMENT", "Text offset must be a UTF-8 boundary within textBytes")
	}
	end := offset + max
	if end > len(s) {
		end = len(s)
	}
	for end < len(s) && end > offset && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[offset:end], end, nil
}
