package objective

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/eeyung1/multi-agent-orchestrator/internal/state"
)

type Task struct {
	ID        string   `json:"id"`
	Feature   string   `json:"feature"`
	Scope     string   `json:"scope"`
	DependsOn []string `json:"depends_on,omitempty"`
	Priority  int      `json:"priority,omitempty"`
}

type Plan struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	Tasks     []Task `json:"tasks"`
}

func Validate(primary string, plan Plan) error {
	if strings.TrimSpace(plan.ID)=="" || strings.TrimSpace(plan.Objective)=="" { return errors.New("plan id and objective are required") }
	seen:=map[string]Task{}
	for _,t:=range plan.Tasks {
		if strings.TrimSpace(t.ID)=="" || strings.TrimSpace(t.Feature)=="" { return errors.New("task id and feature are required") }
		if _,ok:=seen[t.ID];ok{return fmt.Errorf("duplicate task id: %s",t.ID)}
		scope,err:=state.CanonicalScope(primary,t.Scope);if err!=nil{return fmt.Errorf("task %s scope: %w",t.ID,err)}
		t.Scope=scope;seen[t.ID]=t
	}
	for _,t:=range plan.Tasks {
		for _,dep:=range t.DependsOn {
			if dep==t.ID{return fmt.Errorf("task %s depends on itself",t.ID)}
			if _,ok:=seen[dep];!ok{return fmt.Errorf("task %s has unknown dependency: %s",t.ID,dep)}
		}
	}
	if hasCycle(plan.Tasks){return errors.New("task dependency graph contains a cycle")}
	return nil
}

func Eligible(plan Plan, completed map[string]bool, activeScopes []string) []Task {
	var out []Task
	for _,t:=range plan.Tasks {
		if completed[t.ID]{continue}
		ready:=true
		for _,d:=range t.DependsOn{if !completed[d]{ready=false;break}}
		if !ready{continue}
		conflict:=false
		for _,s:=range activeScopes{if overlap(t.Scope,s){conflict=true;break}}
		if !conflict{out=append(out,t)}
	}
	sort.SliceStable(out,func(i,j int)bool{
		if out[i].Priority==out[j].Priority{return out[i].ID<out[j].ID}
		return out[i].Priority>out[j].Priority
	})
	return out
}

func hasCycle(tasks []Task) bool {
	deps:=map[string][]string{};for _,t:=range tasks{deps[t.ID]=t.DependsOn}
	visiting:=map[string]bool{};done:=map[string]bool{}
	var visit func(string)bool
	visit=func(id string)bool{if visiting[id]{return true};if done[id]{return false};visiting[id]=true;for _,d:=range deps[id]{if visit(d){return true}};visiting[id]=false;done[id]=true;return false}
	for id:=range deps{if visit(id){return true}};return false
}
func overlap(a,b string)bool{a=strings.Trim(strings.TrimSpace(a),"/");b=strings.Trim(strings.TrimSpace(b),"/");return a=="."||b=="."||a==b||strings.HasPrefix(a,b+"/")||strings.HasPrefix(b,a+"/")}
