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
6. If the tool call is truly idempotent, that is it's pure or it uses an
   idempotency key, then it should be marked as idempotent. This enables agents
& harnesses to quickly a decision on retrying failed tool calls.

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
- reversible means that it would be trivial to undo the consequences at a later time.
- irreversible means that it would be hard or impossible to undo the consequences at a later time.
//TODO: format this into a table with func, effect, and explanation of why it's read-only/reversible/irreversible
- e.g. sending an email is irreversible. creating a draft email is reversible. deleting an email is irreversible.
- . sending a tweet is irreversible, even if is deletable (it is made public and therefore cannot be undone).
- It should be readOnly, reversible, or irreversible.
  
IMPORTANT: Build something that wouldn't embarrass the author if publicly published.
