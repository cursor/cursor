#!/usr/bin/env node
/**
 * Interactive SDK login for hosts without CURSOR_API_KEY.
 * Opens (or prints) a Cursor login URL and stores a minted key in ~/.cursor/sdk/auth.json.
 */
import { Cursor } from "@cursor/sdk";

const status = await Cursor.auth.status();
if (status.status === "logged-in") {
  console.log(`Already logged in${status.email ? ` as ${status.email}` : ""}.`);
  process.exit(0);
}

console.log("Waiting for browser login…");
const result = await Cursor.auth.login({
  apiKeyName: "okaybrah/cursor sdk-login",
  onLoginUrl: (url) => {
    console.log("\nOpen this URL to authorize the Cursor SDK:\n");
    console.log(url);
    console.log("");
  },
});

console.log(
  `Logged in${result.email ? ` as ${result.email}` : ""}. You can now run: npm run docs:health`
);
