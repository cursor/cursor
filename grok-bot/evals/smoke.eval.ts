import { defineEval } from "@grok/agent-sdk/evals";

export default defineEval({
  tags: ["smoke"],
  description: "The echo tool returns the message it was given.",
  async test(t) {
    await t.send('Use the echo tool to echo "hello".');
    t.succeeded();
    t.calledTool("echo");
  },
});
