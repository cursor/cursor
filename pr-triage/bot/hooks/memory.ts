// Every session and every user of this agent share one memory journal.
// To turn memory off, delete this file and the "## Memory" section of
// instructions.md.
import { memoryHook } from "@grok/agent-sdk/memory";

export default memoryHook();
