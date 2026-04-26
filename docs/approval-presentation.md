# Approval Presentation for Tools

Toolbox tools can provide a safe, structured approval summary for the local approval console by exporting `displayApproval` from the same TypeScript file as the tool.

Some older notes may call this a render approval function. The implemented export name is `displayApproval`.

## Export Shape

```ts
export default async function tool(to: string, subject: string, body: string) {
  // normal tool implementation
}

export function displayApproval(to: string, subject: string, body: string) {
  return {
    schema: "toolbox.approval.presentation.v1",
    description: "Send Gmail message.",
    blocks: [
      {
        type: "fields",
        fields: [
          { label: "To", value: to, kind: "email", copyable: true },
          { label: "Subject", value: subject },
          { label: "Body", value: body, multiline: true },
        ],
      },
    ],
  };
}
```

`displayApproval` receives the same parameters as the default-exported tool function. If the tool is `tool(input)`, then the presenter should be `displayApproval(input)`. If the tool is `tool(a, b)`, then the presenter should be `displayApproval(a, b)`.

The presenter parameter types must match `Parameters<typeof tool>`. Toolbox checks this at TypeScript compile time, but it does not constrain the presenter return type to the tool return type. `displayApproval` should return either `null`/`undefined` to use the default raw parameter display, or an approval presentation object.

## Runtime Rules

Toolbox runs `displayApproval` before storing the pending approval. The result is stored with the approval record, so the user reviews the presentation that corresponds to the reviewed params and tool fingerprint.

The presentation runner is intentionally restricted:

- no `fetch`
- no `exec`
- no filesystem host functions
- no credential injection
- no package-provided HTML
- no side-effecting tool execution

If `displayApproval` is missing, throws, times out, or returns invalid data, Toolbox falls back to `params_inspect`.

## Schema

```ts
type ApprovalPresentation = {
  schema: "toolbox.approval.presentation.v1";
  title?: string;
  description?: string;
  blocks?: ApprovalPresentationBlock[];
};

type ApprovalPresentationBlock = FieldsBlock | TextBlock | ListBlock;

type FieldsBlock = {
  type: "fields";
  title?: string;
  fields: ApprovalField[];
};

type ApprovalField = {
  label: string;
  value: string;
  kind?: "text" | "email" | "date" | "time" | "datetime" | "duration" | "path" | "id";
  multiline?: boolean;
  copyable?: boolean;
};

type TextBlock = {
  type: "text";
  title?: string;
  text: string;
  multiline?: boolean;
  monospace?: boolean;
  copyable?: boolean;
};

type ListBlock = {
  type: "list";
  title?: string;
  items: string[];
};
```

## Rendering Behavior

The approval console owns all rendering. Tool packages only provide structured text data.

- The row summary uses the first important `fields` values.
- Expanded details always include raw `params_inspect`.
- All package-provided strings are HTML-escaped.
- Raw params are rendered as text, never as HTML.

## Guidance

Keep summaries short and concrete. Prefer fields that let a human decide quickly:

- email: `To`, `Subject`, `Body`
- calendar: `Title`, `When`, `Guests`
- issue trackers: `Repository`, `Issue`, `Action`
- filesystem: `Path`, `Operation`

Do not include secrets, access tokens, or large unbounded payloads in presentation fields. Use `params_inspect` for canonical raw review data and concise presentation fields for human scanning.
