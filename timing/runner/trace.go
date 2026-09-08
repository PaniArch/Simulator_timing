package runner

import "vortex.local/simulator/timing/model"

// StageEvent refers to post-edge residents, so enter/leave denotes a transfer
// committed on Cycle. Context aliases in different resources are intentional.
type StageEvent struct {
	Resource            string
	Kind                string
	Token               model.Token
	Reason              string
	Position, Remaining int
}
type residentKey struct {
	resource        string
	id, epoch       uint64
	warp, uop, mask uint8
}

func key(resource string, t model.Token) residentKey {
	return residentKey{resource, t.ID, t.Epoch, t.Warp, t.Uop, t.Mask}
}
func stageEvents(before, after []model.ResourceState) []StageEvent {
	old := map[residentKey]model.Resident{}
	next := map[residentKey]bool{}
	for _, resource := range before {
		for _, entry := range resource.Residents {
			old[key(resource.ID, entry.Token)] = entry
		}
	}
	events := []StageEvent{}
	for _, resource := range after {
		for _, entry := range resource.Residents {
			k := key(resource.ID, entry.Token)
			next[k] = true
			kind, reason := "enter", entry.Reason
			if previous, exists := old[k]; exists {
				kind = "stay"
				if previous.Position != entry.Position || previous.Remaining != entry.Remaining {
					kind = "advance"
				} else if entry.Reason == "execution-latency" && resource.Output.Valid {
					reason = "pipeline-output-blocked"
				}
			}
			events = append(events, StageEvent{resource.ID, kind, entry.Token, reason, entry.Position, entry.Remaining})
		}
	}
	for _, resource := range before {
		for _, entry := range resource.Residents {
			if !next[key(resource.ID, entry.Token)] {
				events = append(events, StageEvent{Resource: resource.ID, Kind: "leave", Token: entry.Token})
			}
		}
	}
	return events
}
func boundaryEvents(report model.CoreReport) []StageEvent {
	events := []StageEvent{}
	add := func(resource, kind string, s model.Signal) {
		if s.Valid {
			events = append(events, StageEvent{Resource: resource, Kind: kind, Token: s.Token})
		}
	}
	add("operands", "read", report.Read)
	for _, s := range report.Executed {
		add("execute", "accept", s)
	}
	add("writeback", "complete", report.Writeback)
	add("pending", "complete", report.PendingRelease)
	add("branch", "feedback", report.Branch)
	add("control", "feedback", report.Control)
	add("fflags", "feedback", report.Flags)
	if report.MemoryAccepted {
		add("memory-service", "accept", report.MemoryRequest)
	}
	return events
}
