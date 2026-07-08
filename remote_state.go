package tfstateref

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fujiwara/tfstate-lookup/tfstate"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// tfstateLookup wraps the tfstate.TFState for caching.
type tfstateLookup = tfstate.TFState

// buildRemoteState scans all .tf files to collect terraform_remote_state
// blocks and build label → state URL mappings.
func (c *Checker) buildRemoteState() error {
	return c.walkDirs(func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".tf" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		dir := filepath.Dir(path)
		for _, entry := range parseRemoteStateBlocks(content, path) {
			c.stateRefs = append(c.stateRefs, stateRef{File: path, Label: entry.Label, URL: entry.URL})
			c.labelToStateURL[dir+"\t"+entry.Label] = entry.URL
		}

		return nil
	})
}

// getState reads a tfstate from its URL (with caching).
func (c *Checker) getState(ctx context.Context, stateURL string) (*tfstateLookup, error) {
	if state, ok := c.stateCache[stateURL]; ok {
		return state, nil
	}
	if err, ok := c.stateErrors[stateURL]; ok {
		return nil, err
	}

	state, err := tfstate.ReadURL(ctx, stateURL)
	if err != nil {
		c.stateErrors[stateURL] = err
		return nil, err
	}

	c.stateCache[stateURL] = state
	return state, nil
}

// runRemoteStateChecks executes state-existence and output-reference checks.
func (c *Checker) runRemoteStateChecks(ctx context.Context, result *Result) {
	if len(c.labelToStateURL) == 0 {
		c.log("warning: no remote_state references found in %v", c.dirs)
		return
	}

	c.log("Found %d remote_state label mappings in %v", len(c.labelToStateURL), c.dirs)

	c.log("=== state-existence: remote_state existence ===")
	se := c.checkStateExistence(ctx)
	result.StateExistence = &se

	c.log("")
	c.log("=== output-reference: remote_state output references ===")
	or := c.checkOutputReference(ctx)
	result.OutputReference = &or

	result.Errors = append(result.Errors, se.Errors...)
	result.Errors = append(result.Errors, or.Errors...)
}

// checkStateExistence verifies that every terraform_remote_state block's
// state actually exists at the referenced location.
func (c *Checker) checkStateExistence(ctx context.Context) CheckResult {
	result := CheckResult{Errors: []CheckError{}}

	// track already-checked state URLs to avoid duplicate checks
	checked := make(map[string]bool)

	for _, ref := range c.stateRefs {
		if checked[ref.URL] {
			continue
		}
		checked[ref.URL] = true
		result.Checked++

		if _, err := c.getState(ctx, ref.URL); err == nil {
			c.logVerbose("  ok: %s -> %q state exists at %s", ref.File, ref.Label, ref.URL)
		} else {
			msg := fmt.Sprintf(
				"terraform_remote_state %q references %s, but the state could not be read",
				ref.Label, ref.URL,
			)
			c.log("  error: %s: %s", ref.File, msg)
			result.Errors = append(result.Errors, CheckError{File: ref.File, Message: msg})
		}
	}

	c.log("Checked %d unique state(s)", result.Checked)
	return result
}

// checkOutputReference verifies that every data.terraform_remote_state.X.outputs.Y
// reference points to an output that actually exists in the remote state.
func (c *Checker) checkOutputReference(ctx context.Context) CheckResult {
	result := CheckResult{Errors: []CheckError{}}

	walkErr := c.walkDirs(func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".tf" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		refs := extractOutputReferences(content, path)
		for _, ref := range refs {
			result.Checked++
			sourceDir := filepath.Dir(path)

			// resolve remote_state label → state URL
			mapKey := sourceDir + "\t" + ref.Label
			stateURL, ok := c.labelToStateURL[mapKey]
			if !ok {
				c.logVerbose("  skip: %s -> label %q not found in label map", path, ref.Label)
				continue
			}

			// fetch the state (cached or read)
			state, stateErr := c.getState(ctx, stateURL)
			if stateErr != nil {
				// already reported by state-existence, so skip
				c.logVerbose("  skip: %s -> state %s not available", path, stateURL)
				continue
			}

			// verify the output exists (including nested attributes)
			lookupKey := fmt.Sprintf("output.%s", ref.OutputName)
			attrs, lookupErr := state.Lookup(lookupKey)
			if lookupErr != nil || attrs == nil || attrs.Value == nil {
				msg := fmt.Sprintf(
					"data.terraform_remote_state.%s.outputs.%s references %q, but it does not exist in state %s",
					ref.Label, ref.OutputName, lookupKey, stateURL,
				)
				c.log("  error: %s: %s", path, msg)
				result.Errors = append(result.Errors, CheckError{File: path, Message: msg})
			} else {
				c.logVerbose("  ok: %s -> data.terraform_remote_state.%s.outputs.%s (state: %s)", path, ref.Label, ref.OutputName, stateURL)
			}
		}

		return nil
	})
	if walkErr != nil {
		c.log("warning: %v", walkErr)
	}

	c.log("Checked %d output reference(s)", result.Checked)
	return result
}

// ==============================================================================
// HCL parser
// ==============================================================================

// remoteStateEntry holds a parsed remote_state block's label and the URL of
// the state it references (in a form tfstate.ReadURL understands).
type remoteStateEntry struct {
	Label string
	URL   string
}

// stateRef is a terraform_remote_state block found in a .tf file.
type stateRef struct {
	File  string
	Label string
	URL   string
}

// outputReference holds info about a data.terraform_remote_state.X.outputs.Y reference.
type outputReference struct {
	File       string
	Label      string
	OutputName string
}

// parseRemoteStateBlocks extracts label and state URL from each
// data "terraform_remote_state" block in a .tf file. Blocks whose
// backend/config cannot be resolved statically (non-literal values,
// unsupported backend) are skipped.
func parseRemoteStateBlocks(content []byte, filename string) []remoteStateEntry {
	f, diags := hclsyntax.ParseConfig(content, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil
	}

	var entries []remoteStateEntry
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}

	for _, block := range body.Blocks {
		if block.Type != "data" || len(block.Labels) < 2 || block.Labels[0] != "terraform_remote_state" {
			continue
		}
		label := block.Labels[1]

		backendType, ok := attrStringLiteral(block.Body, "backend")
		if !ok {
			continue
		}
		workspace, _ := attrStringLiteral(block.Body, "workspace")

		configAttr, exists := block.Body.Attributes["config"]
		if !exists {
			continue
		}
		config := literalObjectValues(configAttr.Expr)

		url, err := buildStateURL(backendType, config, workspace, filepath.Dir(filename))
		if err != nil {
			continue
		}

		entries = append(entries, remoteStateEntry{
			Label: label,
			URL:   url,
		})
	}

	return entries
}

// attrStringLiteral returns the value of a string-literal attribute.
func attrStringLiteral(body *hclsyntax.Body, name string) (string, bool) {
	attr, exists := body.Attributes[name]
	if !exists {
		return "", false
	}
	return extractStringLiteral(attr.Expr)
}

// literalObjectValues evaluates an object expression into a map, keeping only
// items whose key and value are literals. Non-literal items (e.g. values
// referencing variables) are dropped individually so the rest of the config
// is still usable. Nested objects are converted recursively.
func literalObjectValues(expr hclsyntax.Expression) map[string]any {
	objExpr, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}

	config := make(map[string]any)
	for _, item := range objExpr.Items {
		keyVal, diags := item.KeyExpr.Value(nil)
		if diags.HasErrors() || keyVal.Type() != cty.String {
			continue
		}
		val, diags := item.ValueExpr.Value(nil)
		if diags.HasErrors() {
			continue
		}
		if v := ctyToAny(val); v != nil {
			config[keyVal.AsString()] = v
		}
	}
	return config
}

// ctyToAny converts a literal cty value to a Go value. Only strings and
// (nested) objects/maps are needed for backend configs; other types return nil.
func ctyToAny(val cty.Value) any {
	if val.IsNull() || !val.IsKnown() {
		return nil
	}
	switch {
	case val.Type() == cty.String:
		return val.AsString()
	case val.Type().IsObjectType() || val.Type().IsMapType():
		m := make(map[string]any)
		for k, v := range val.AsValueMap() {
			if converted := ctyToAny(v); converted != nil {
				m[k] = converted
			}
		}
		return m
	default:
		return nil
	}
}

// extractOutputReferences extracts all data.terraform_remote_state.X.outputs.Y
// references from an HCL file using AST walking.
func extractOutputReferences(content []byte, filename string) []outputReference {
	f, diags := hclsyntax.ParseConfig(content, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil
	}

	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil
	}

	var refs []outputReference
	seen := make(map[string]bool)

	addRef := func(label, outputName string) {
		key := label + "\t" + outputName
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, outputReference{
			File:       filename,
			Label:      label,
			OutputName: outputName,
		})
	}

	// Collect locals that alias remote_state outputs first, e.g.
	//   batch_cluster = try(data.terraform_remote_state.workflow.outputs.batch_cluster, {})
	// so that indirect references like lookup(local.batch_cluster, "KEY", "")
	// can be tracked down to the map key (batch_cluster.KEY). Only same-file
	// locals are resolved.
	localAliases := collectRemoteStateLocalAliases(body)

	vars := collectAllVariables(body)
	for _, trav := range vars {
		if label, outputName, ok := matchRemoteStateOutputTraversal(trav); ok {
			addRef(label, outputName)
			continue
		}
		// local.ALIAS.KEY / local.ALIAS["KEY"] (ALIAS aliases a remote_state output)
		if label, outputName, ok := matchLocalAliasTraversal(trav, localAliases); ok {
			addRef(label, outputName)
		}
	}

	hclsyntax.VisitAll(body, func(node hclsyntax.Node) hcl.Diagnostics {
		funcCall, ok := node.(*hclsyntax.FunctionCallExpr)
		if !ok || funcCall.Name != "lookup" || len(funcCall.Args) < 2 {
			return nil
		}

		scopeExpr, ok := funcCall.Args[0].(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			return nil
		}

		outputName, ok := extractStringLiteral(funcCall.Args[1])
		if !ok {
			return nil
		}

		// form 1: lookup(data.terraform_remote_state.X.outputs, "KEY", ...)
		if label, ok := matchRemoteStateOutputsTraversal(scopeExpr.Traversal); ok {
			addRef(label, outputName)
			return nil
		}

		// form 2: lookup(local.ALIAS, "KEY", ...) — ALIAS aliases a remote_state output
		if name, rest, ok := matchLocalTraversal(scopeExpr.Traversal); ok && len(rest) == 0 {
			if alias, found := localAliases[name]; found {
				addRef(alias.label, alias.outputPath+"."+outputName)
			}
		}
		return nil
	})

	return refs
}

// localAliasRef records that a local value aliases
// data.terraform_remote_state.<label>.outputs.<outputPath>.
type localAliasRef struct {
	label      string
	outputPath string
}

// collectRemoteStateLocalAliases collects local definitions of the form
//
//	NAME = data.terraform_remote_state.LABEL.outputs.PATH
//	NAME = try(data.terraform_remote_state.LABEL.outputs.PATH, <default>)
//
// returning NAME -> {label, outputPath}. Only same-file locals are resolved;
// cross-file aliases are intentionally not tracked (no false positives).
func collectRemoteStateLocalAliases(body *hclsyntax.Body) map[string]localAliasRef {
	aliases := make(map[string]localAliasRef)
	for _, block := range body.Blocks {
		if block.Type != "locals" {
			continue
		}
		for name, attr := range block.Body.Attributes {
			expr := unwrapTry(attr.Expr)
			scopeExpr, ok := expr.(*hclsyntax.ScopeTraversalExpr)
			if !ok {
				continue
			}
			if label, outputPath, ok := matchRemoteStateOutputTraversal(scopeExpr.Traversal); ok {
				aliases[name] = localAliasRef{label: label, outputPath: outputPath}
			}
		}
	}
	return aliases
}

// unwrapTry returns the first argument of a try(...) call, otherwise the expr
// itself. Used so a local aliased via try(<ref>, <default>) is still resolved.
func unwrapTry(expr hclsyntax.Expression) hclsyntax.Expression {
	if fc, ok := expr.(*hclsyntax.FunctionCallExpr); ok && fc.Name == "try" && len(fc.Args) >= 1 {
		return fc.Args[0]
	}
	return expr
}

// matchLocalTraversal matches local.NAME[.REST...] and returns NAME and the
// remaining dotted parts (REST). REST is empty for a bare local.NAME.
func matchLocalTraversal(trav hcl.Traversal) (string, []string, bool) {
	if len(trav) < 2 {
		return "", nil, false
	}
	root, ok := trav[0].(hcl.TraverseRoot)
	if !ok || root.Name != "local" {
		return "", nil, false
	}
	attr1, ok := trav[1].(hcl.TraverseAttr)
	if !ok {
		return "", nil, false
	}
	return attr1.Name, traversalParts(trav[2:]), true
}

// traversalParts converts traversal steps into dotted path parts, stopping at
// the first step that cannot be represented statically (e.g. a dynamic index
// like [0] or [var.key]).
func traversalParts(steps hcl.Traversal) []string {
	var parts []string
	for _, step := range steps {
		switch s := step.(type) {
		case hcl.TraverseAttr:
			parts = append(parts, s.Name)
		case hcl.TraverseIndex:
			if s.Key.Type() != cty.String {
				return parts
			}
			parts = append(parts, s.Key.AsString())
		default:
			return parts
		}
	}
	return parts
}

// matchLocalAliasTraversal resolves local.ALIAS.KEY... where ALIAS aliases a
// remote_state output, returning (label, "outputPath.KEY..."). Returns false
// for a bare local.ALIAS with no further path (nothing to verify yet).
func matchLocalAliasTraversal(trav hcl.Traversal, aliases map[string]localAliasRef) (string, string, bool) {
	name, rest, ok := matchLocalTraversal(trav)
	if !ok || len(rest) == 0 {
		return "", "", false
	}
	alias, found := aliases[name]
	if !found {
		return "", "", false
	}
	return alias.label, alias.outputPath + "." + strings.Join(rest, "."), true
}

// collectAllVariables recursively collects all variable references from a body.
func collectAllVariables(body *hclsyntax.Body) []hcl.Traversal {
	var traversals []hcl.Traversal
	for _, attr := range body.Attributes {
		traversals = append(traversals, attr.Expr.Variables()...)
	}
	for _, block := range body.Blocks {
		traversals = append(traversals, collectAllVariables(block.Body)...)
	}
	return traversals
}

// matchRemoteStateOutputTraversal checks if a traversal matches
// data.terraform_remote_state.X.outputs.Y[.Z.W...] and returns the full
// dotted path from outputs onward (e.g. "Y.Z.W").
func matchRemoteStateOutputTraversal(trav hcl.Traversal) (string, string, bool) {
	if len(trav) < 5 {
		return "", "", false
	}

	root, ok := trav[0].(hcl.TraverseRoot)
	if !ok || root.Name != "data" {
		return "", "", false
	}
	attr1, ok := trav[1].(hcl.TraverseAttr)
	if !ok || attr1.Name != "terraform_remote_state" {
		return "", "", false
	}
	attr2, ok := trav[2].(hcl.TraverseAttr)
	if !ok {
		return "", "", false
	}
	label := attr2.Name

	attr3, ok := trav[3].(hcl.TraverseAttr)
	if !ok || attr3.Name != "outputs" {
		return "", "", false
	}

	// Build the full dotted path from trav[4] onward
	parts := traversalParts(trav[4:])
	if len(parts) == 0 {
		return "", "", false
	}

	return label, strings.Join(parts, "."), true
}

// matchRemoteStateOutputsTraversal checks if a traversal matches
// data.terraform_remote_state.X.outputs (exactly 4 elements).
func matchRemoteStateOutputsTraversal(trav hcl.Traversal) (string, bool) {
	if len(trav) != 4 {
		return "", false
	}

	root, ok := trav[0].(hcl.TraverseRoot)
	if !ok || root.Name != "data" {
		return "", false
	}
	attr1, ok := trav[1].(hcl.TraverseAttr)
	if !ok || attr1.Name != "terraform_remote_state" {
		return "", false
	}
	attr2, ok := trav[2].(hcl.TraverseAttr)
	if !ok {
		return "", false
	}
	attr3, ok := trav[3].(hcl.TraverseAttr)
	if !ok || attr3.Name != "outputs" {
		return "", false
	}

	return attr2.Name, true
}

// extractStringLiteral extracts a string value from an HCL expression.
func extractStringLiteral(expr hclsyntax.Expression) (string, bool) {
	val, diags := expr.Value(nil)
	if diags.HasErrors() {
		return "", false
	}
	if val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}
