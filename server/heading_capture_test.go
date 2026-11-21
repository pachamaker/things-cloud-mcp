package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Only the documented native seeds are exported. This collector never creates
// evidence by calling the server's write tools (protocol section 8, KI-45).
func buildHeadingCapture(items []wireItem) (captureFixture, error) {
	live := foldHistory(items)
	titles := []string{"SEED project", "SEED heading", "SEED under heading"}
	roles := map[string]string{}
	fx := captureFixture{Redacted: []string{}}
	for i, title := range titles {
		candidates := seedCandidates(live, title, "Task", func(e *foldedEntity) bool {
			tp, ok := jsonInt(e.Fields["tp"])
			want := []int64{1, 2, 0}[i]
			return ok && tp == want && isJSONNull(e.Fields["rr"]) && len(jsonIDs(e.Fields["rt"])) == 0
		})
		if len(candidates) != 1 {
			return captureFixture{}, fmt.Errorf("want exactly one native %q seed, found %d", title, len(candidates))
		}
		id := candidates[0].UUID
		roles[id] = title
		fx.Objects = append(fx.Objects, captureObject{UUID: id, Role: title})
	}

	project, heading, task := fx.Objects[0].UUID, fx.Objects[1].UUID, fx.Objects[2].UUID
	if refs := jsonIDs(live[heading].Fields["pr"]); len(refs) != 1 || refs[0] != project {
		return captureFixture{}, fmt.Errorf("SEED heading does not belong to SEED project")
	}
	if refs := jsonIDs(live[task].Fields["agr"]); len(refs) != 1 || refs[0] != heading {
		return captureFixture{}, fmt.Errorf("SEED under heading is not under SEED heading")
	}
	slots := map[int]int{}
	for _, it := range items {
		slots[it.ServerIndex]++
	}
	selected := []wireItem{}
	for _, it := range items {
		if roles[it.UUID] != "" {
			selected = append(selected, it)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].ServerIndex != selected[j].ServerIndex {
			return selected[i].ServerIndex < selected[j].ServerIndex
		}
		return selected[i].UUID < selected[j].UUID
	})
	for i, it := range selected {
		p, err := sanitizeCapturePayload(it.Payload, fmt.Sprintf("items[%d].p", i), func(id string) bool { return roles[id] != "" }, &fx.Redacted)
		if err != nil {
			return captureFixture{}, err
		}
		fx.Items = append(fx.Items, captureItem{Index: it.ServerIndex, SlotItems: slots[it.ServerIndex], UUID: it.UUID, Role: roles[it.UUID], Action: it.Action, Kind: it.Kind, Payload: p})
	}
	return fx, nil
}

// Fail closed: a final-state snapshot or MCP-generated mock is not evidence of
// native creation AND assignment. The exact membership tuple must be observed.
func checkHeadingEvidence(fx captureFixture) error {
	ids := map[string]string{}
	for _, o := range fx.Objects {
		if ids[o.Role] != "" {
			return fmt.Errorf("duplicate native seed role")
		}
		ids[o.Role] = o.UUID
	}
	project, heading, task := ids["SEED project"], ids["SEED heading"], ids["SEED under heading"]
	if !isBase58UUID(project) || !isBase58UUID(heading) || !isBase58UUID(task) {
		return fmt.Errorf("missing native heading seed identifiers")
	}
	created, moved := false, false
	for _, it := range fx.Items {
		if it.UUID != task {
			continue
		}
		var p map[string]json.RawMessage
		if err := json.Unmarshal(it.Payload, &p); err != nil {
			return fmt.Errorf("invalid captured payload")
		}
		a, b := jsonIDs(p["agr"]), jsonIDs(p["pr"])
		var ar []string
		areaOK := json.Unmarshal(p["ar"], &ar) == nil && ar != nil && len(ar) == 0
		if len(a) != 1 || a[0] != heading || len(b) != 1 || b[0] != project || !areaOK {
			continue
		}
		if it.Action == 0 {
			created = true
		}
		if it.Action == 1 {
			moved = true
		}
	}
	if !created || !moved {
		return fmt.Errorf("native evidence needs both create and modify with agr=[heading], pr=[project], ar=[]; creation=%t move=%t; inspect the capture and adjust the encoder to native behavior before enabling heading writes", created, moved)
	}
	return nil
}
