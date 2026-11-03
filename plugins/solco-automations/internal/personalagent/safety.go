package personalagent

// SandboxEnforced says whether Solco can confine an Agent. It cannot: Herdr
// Agents run with the person's own permissions, and Shepherdr's Linux
// bubblewrap isolation has no macOS counterpart here. So every limit Solco
// puts on a task ("read-only", "no push") is an instruction in the prompt plus
// the absence of any Solco code path that merges, pushes or deletes — never a
// promise that the Agent cannot. A person approves every merge, and the words
// shown to people say "instruction", not "guarantee".
const SandboxEnforced = false

// SafetyNote is the sentence cards and docs use for those limits.
const SafetyNote = "Read-only and review-first limits are instructions to the Agent, not an enforced sandbox."
