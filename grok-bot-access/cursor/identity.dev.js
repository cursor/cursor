// Cursor app identity: the simulated viewer for local development. Yours to edit; add-app-identity seeds this file once and never rewrites it.
// While neither CURSOR_APP_ID nor VERCEL is set, which is only your own dev server, a request without the identity header reads as this viewer, with simulated: true and an empty token. Nothing verified it. A published app never reads this file: the gate's signed header is the only viewer it trusts.
// Private-app shape: no teamId / teamRole (matches publish visibility). Never write a real person's identity here.

/** @type {import("./identity.js").DevViewer | null} */
export const DEV_VIEWER = {
  sub: "user:dev",
  email: "dev@example.com",
  name: "Dev Viewer",
};
