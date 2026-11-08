# Call the API

`api/openapi.yaml` is the contract, and the running API also serves it at `/openapi.json`. Generate a client from it in any language.

## From Go

`oapi-codegen` already generates a typed client in `internal/apiclient`, which the tests use. It's internal to this module, so in another Go project, generate your own with the same config:

```sh
go get -tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2
curl -o openapi.yaml http://localhost:3000/openapi.json
go tool oapi-codegen -generate client,models -package apiclient -o client.gen.go openapi.yaml
```

Then call it with a token:

```go
client, err := apiclient.NewClientWithResponses("http://localhost:3000",
	apiclient.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}))
if err != nil {
	return err
}
res, err := client.CreateNoteWithResponse(ctx, nil, apiclient.CreateNote{Title: "Typed"})
if err != nil {
	return err
}
if res.JSON201 != nil {
	fmt.Println(res.JSON201.Id)
} else {
	fmt.Println(res.StatusCode(), string(res.Body))
}
```

Each response has a typed field per status in the spec, such as `JSON201` or `ApplicationproblemJSON404`, so error bodies are typed as Problem Details too.

## From other languages

Point a generator at the spec, such as [openapi-typescript](https://openapi-ts.dev/) for TypeScript or [openapi-python-client](https://github.com/openapi-generators/openapi-python-client) for Python. Regenerate the client whenever `api/openapi.yaml` changes.
