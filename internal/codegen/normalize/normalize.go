package normalize

import (
	"cmp"
	"slices"

	"github.com/lathe-cli/lathe/internal/codegen/rawir"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func Normalize(mod *rawir.RawModule) []runtime.CommandSpec {
	trim := commonNoisePrefix(mod.Operations)
	var specs []runtime.CommandSpec
	for _, op := range mod.Operations {

		opID := op.OperationID
		useName := ""
		if opID == "" {
			opID = synthOperationID(op.Method, op.Path)
			useName = synthUseName(op.Method, op.Path, trim)
		} else {
			useName = kebabFromID(opNameFromID(opID, group(op), mod.Name))
		}
		if opID == "" || useName == "" {
			continue
		}
		spec := runtime.CommandSpec{
			Group:       group(op),
			Use:         useName,
			Short:       pickShort(op),
			OperationID: opID,
			Method:      op.Method,
			PathTpl:     joinBasePath(op.ServerBasePath, op.Path),
		}
		for _, pp := range op.Parameters {
			switch pp.In {
			case runtime.InPath, runtime.InQuery, runtime.InHeader, runtime.InFormData, runtime.InVariable:
				spec.Params = append(spec.Params, parameter(pp))
			}
		}
		if op.RequestBody != nil {
			spec.RequestBody = &runtime.RequestBody{
				Required:  op.RequestBody.Required,
				MediaType: op.RequestBody.MediaType,
				Schema:    runtimeSchema(op.RequestBody.Schema, mod.Schemas, map[string]bool{}),
				Template:  op.RequestBody.Template,
				MergePath: op.RequestBody.MergePath,
			}
			bodyParams := multipartBodyParams(op.RequestBody, mod.Schemas)
			disambiguateMultipartParamFlags(spec.Params, bodyParams)
			spec.Params = append(spec.Params, bodyParams...)
		}
		normalizeParamFlags(spec.Params)
		lp, itemRef := deriveList(op, mod.Schemas)
		spec.Output.ListPath = lp
		if itemRef != "" {
			spec.Output.DefaultColumns = defaultColumns(itemRef, mod.Schemas)
		}
		spec.Output.ResponseMediaType = deriveResponseMediaType(op)
		spec.Output.Pagination = derivePagination(op, mod.Schemas)
		spec.Output.Streaming = deriveStreaming(op)
		applyRawOutputHints(&spec, op.Output)
		spec.Security = deriveSecurity(op)
		specs = append(specs, spec)
	}
	slices.SortFunc(specs, func(a, b runtime.CommandSpec) int {
		return cmp.Or(cmp.Compare(a.Group, b.Group), cmp.Compare(a.Use, b.Use), cmp.Compare(a.OperationID, b.OperationID), cmp.Compare(a.PathTpl, b.PathTpl), cmp.Compare(a.Method, b.Method))
	})
	return specs
}
