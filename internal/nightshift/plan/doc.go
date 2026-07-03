// Package plan holds nightshift's pure decision logic — the "what should
// happen" with no side effects, split out from the stateful orchestrator so it
// can't accrete git/process/filesystem dependencies and stays cheap to unit
// test. Four concerns live here: the per-turn assignment policy (PickTurn /
// AssignRound), the CI-scope safety check (CIScopeUnsafe), the green-gate's
// interpreter selection + verdict classification (PickGatePython, ClassifyPytest,
// ClassifyBase), and the fixed-set fence's list parsing + selection resolution
// (ListIDs, NormalizeOnly, ResolveOnly, InOnly). The nightshift root package
// does the I/O and calls into these.
package plan
