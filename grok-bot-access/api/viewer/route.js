import { requireIdentity } from "../../cursor/identity.js";

/**
 * Gated viewer endpoint for the private access UI.
 * Uses requireIdentity (apps-identity): 401 when no verified viewer.
 * Returns only safe fields — never token or claims.
 */
export async function GET(request) {
  const identity = await requireIdentity(request);
  if (identity instanceof Response) {
    // Strip any body that might include internals; keep a stable JSON shape.
    return Response.json(
      {
        verified: false,
        reason: "unauthorized",
      },
      {
        status: 401,
        headers: { "cache-control": "private, no-store" },
      },
    );
  }

  // Keys are not people — label by key name only.
  const label =
    identity.kind === "key"
      ? identity.name || "API key"
      : identity.name || identity.email || "Signed-in viewer";

  return Response.json(
    {
      verified: true,
      name: label,
      kind: identity.kind,
      simulated: Boolean(identity.simulated),
      appVisibility: identity.appVisibility,
      stage: identity.stage ?? null,
    },
    {
      headers: { "cache-control": "private, no-store" },
    },
  );
}
