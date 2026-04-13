# Tool Rules

0. ALWAYS WRITE THE TOOL WITH AN AUDIENCE OF VIEW OF SOMEONE WHO DOESN'T KNOW
   ANYTHING ABOUT THE TOOL PACKAGE.
1. if the package + function name is enough to understand what the tool does,
   don't add a description.
2. if the parameter name + type is enough to understand the parameter, you
   don't need to add a descripton. Sometimes you can make the parameter name
   more a little more descriptive so that the description is not needed.
3. order parameters based on resource heirarchy.
4. when considering a function description, understand that a param might be
   hidden, and thus the description might be wrong.
5. always add a @effect to a function
6. `readOnly` means the tool does not cause side effects. It does not imply
   `@idempotent`.
7. If something advances a cursor, consumes a message, marks data seen, takes a
   lease, or otherwise changes server state, then it is not actually
   `readOnly`.
8. `@idempotent` is stronger. Mark it when the same call with the same inputs
   can be treated as interchangeable for retries, reuse, or refetch decisions
   because the result is pure, tied to immutable input, or protected by an
   idempotency key or equivalent deduplication contract.
9. Do not mark ordinary live reads `@idempotent` just because they are
   `readOnly`. If the backing data may have changed, callers may need to
   refetch instead of reusing an earlier result.
10. If multiple tools share utilities, put the shared code in a helper module
    and include it via `additionalTypeScriptGlobs` in `toolbox.devpkg.json`
    rather than copying the same helper logic into multiple tool entry files.
    `additionalTypeScriptGlobs` supports `**` globs, for example
    `"additionalTypeScriptGlobs": ["lib/**/*.ts"]`.

## Examples

### Example 1: Overuse of comments

This will generate a tool which uses far more tokens that it needs and will be
harder to understand if a parameter is hidden.

BAD:

```TS
//calc.add.ts

/**
 * Add two numbers.
 * @effect readOnly
 * @idempotent
 * @param a - The first number
 * @param b - The second number
 */
export default function add(a: number, b: number): string {
  return String(a + b);
}
```

- If a or b are made hidden, the description is wrong. (i.e. add(b: number) is not adding two numbers)
- calc.add plus the signature is obvious what it does.

GOOD:

```TS
// calc.add.ts

/**
 * @effect readOnly
 * @idempotent
 */
export default function add(a: number, b: number): string {
  return String(a + b);
}
```

## Example 2: Choosing effect

- readOnly means there is no side effect.
- readOnly does not mean the result stays current or can be reused later
  without refetching.
- idempotent means the same input can be treated as yielding the same stable
  answer or equivalent stable contract.
- if a so-called read advances a cursor, consumes an event, records an ack, or
  otherwise changes server state, it is mislabeled and should not be marked
  readOnly.
- reversible means that it would be trivial to undo the consequences at a later time.
- irreversible means that it would be hard or impossible to undo the consequences at a later time.
//TODO: format this into a table with func, effect, and explanation of why it's read-only/reversible/irreversible
- e.g. sending an email is irreversible. creating a draft email is reversible. deleting an email is irreversible.
- . sending a tweet is irreversible, even if is deletable (it is made public and therefore cannot be undone).
- e.g. `calc.add` is readOnly and idempotent.
- e.g. `git.commit.get(sha)` is readOnly and idempotent because the input
  points at immutable data.
- e.g. `issues.get(id)`, `users.list`, and `tickets.search` are usually
  readOnly but not idempotent, because the data may have changed and the caller
  may need to refetch.
- e.g. `payments.charge.create` may be irreversible but still idempotent if
  the API requires an idempotency key and guarantees deduplication.
- e.g. `notifications.poll` is not readOnly if polling advances a server-side
  cursor or lease.
- It should be readOnly, reversible, or irreversible.
  
IMPORTANT: Build something that wouldn't embarrass the author if publicly published.
