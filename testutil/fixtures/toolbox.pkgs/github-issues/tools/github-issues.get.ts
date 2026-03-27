import "./shims.ts";
import { z } from "zod";

const IssueSchema = z.object({
  number: z.number(),
  title: z.string(),
  state: z.string(),
  body: z.string().nullable(),
  labels: z.array(z.object({ name: z.string().optional() })),
  assignees: z.array(z.object({ login: z.string() })).nullable(),
  created_at: z.string(),
  updated_at: z.string(),
});

/**
 * @accessMode readOnly
 * @idempotent
 */
export default async function tool(
  owner: string,
  repo: string,
  number: number,
  token?: string,
) {
  const headers: Record<string, string> = {
    Accept: "application/vnd.github+json",
    "User-Agent": "toolbox",
    "X-GitHub-Api-Version": "2022-11-28",
  };
  if (token) {
    headers["Authorization"] = `token ${token}`;
  }

  const resp = await fetch(
    `https://api.github.com/repos/${owner}/${repo}/issues/${number}`,
    { headers },
  );

  if (resp.status !== 200) {
    const body = await resp.text();
    throw new Error(`GitHub API ${resp.status}: ${body}`);
  }

  const data = await resp.json();
  const issue = IssueSchema.parse(data);

  return JSON.stringify({
    number: issue.number,
    title: issue.title,
    state: issue.state,
    body: issue.body,
    labels: issue.labels.map((l) => l.name).filter(Boolean),
    assignees: (issue.assignees ?? []).map((a) => a.login),
    created_at: issue.created_at,
    updated_at: issue.updated_at,
  });
}
