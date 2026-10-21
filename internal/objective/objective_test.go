package objective

import "testing"

func TestValidateRejectsCyclesAndUnknownDependencies(t *testing.T){
	p:=Plan{ID:"p",Objective:"ship",Tasks:[]Task{{ID:"a",Feature:"a",Scope:"a",DependsOn:[]string{"missing"}}}}
	if Validate(t.TempDir(),p)==nil{t.Fatal("expected unknown dependency rejection")}
	p.Tasks=[]Task{{ID:"a",Feature:"a",Scope:"a",DependsOn:[]string{"b"}},{ID:"b",Feature:"b",Scope:"b",DependsOn:[]string{"a"}}}
	if Validate(t.TempDir(),p)==nil{t.Fatal("expected cycle rejection")}
}
func TestEligibleHonorsDependenciesPriorityAndScopes(t *testing.T){
	p:=Plan{ID:"p",Objective:"ship",Tasks:[]Task{
		{ID:"a",Feature:"a",Scope:"backend",Priority:1},
		{ID:"b",Feature:"b",Scope:"frontend",Priority:3},
		{ID:"c",Feature:"c",Scope:"docs",DependsOn:[]string{"a"},Priority:9},
	}}
	got:=Eligible(p,map[string]bool{},[]string{"frontend/src"})
	if len(got)!=1||got[0].ID!="a"{t.Fatalf("eligible=%v",got)}
	got=Eligible(p,map[string]bool{"a":true},nil)
	if len(got)!=2||got[0].ID!="c"{t.Fatalf("eligible=%v",got)}
}
