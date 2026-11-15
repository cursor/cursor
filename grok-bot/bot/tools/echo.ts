import { defineTool } from "@grok/agent-sdk/tools";
import * as z from "zod/v4";

export default defineTool({
  description: "Echo a short message back (demo tool).",
  inputSchema: z.object({ message: z.string() }),
  async execute({ message }) {
    return { echoed: message };
  },
});
