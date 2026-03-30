/**
 * @effect readOnly
 * @idempotent
 */
const USERS_ENDPOINT = "https://admin.googleapis.com/admin/directory/v1/users?customer=my_customer&maxResults=1&orderBy=email";

interface GoogleWorkspaceUsersResponse {
  users?: Array<{
    primaryEmail?: unknown;
  }>;
}

export default async function tool(): Promise<string> {
  const response = await fetch(USERS_ENDPOINT, {
    headers: {
      Accept: "application/json",
    },
  });

  if (response.status !== 200) {
    throw new Error(`Google Workspace API ${response.status}`);
  }

  const rawBody = await response.text();
  let payload: GoogleWorkspaceUsersResponse;
  try {
    payload = JSON.parse(rawBody) as GoogleWorkspaceUsersResponse;
  } catch {
    throw new Error("google workspace response was not valid JSON");
  }

  const email = payload.users?.[0]?.primaryEmail;
  if (typeof email !== "string" || email.trim() === "") {
    throw new Error("google workspace response missing users[0].primaryEmail");
  }

  return email;
}
