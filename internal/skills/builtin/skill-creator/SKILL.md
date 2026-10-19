---
name: skill-creator
description: Turn a repeatable workflow into a reusable local skill — write a SKILL.md under the workspace skills directory so future sessions can load it by name.
when_to_use: The user asks to save a workflow as a skill, to teach the agent a reusable procedure, or to make a repeated multi-step routine reusable.
---

# Creating a skill

A skill is a directory with a single `SKILL.md` under `.zgo/skills/` in the
workspace (or `~/.zgo/skills/` for user-global). Loading is by exact name;
the body is plain instructions the agent follows verbatim.

## Steps

1. Agree on the skill's **single responsibility**. If it covers two jobs,
   write two skills.
2. Create `.zgo/skills/<kebab-case-name>/SKILL.md` with the write tool.
3. Frontmatter (flat `key: value` between `---` lines):
   - `name`: the directory basename. This is the load key — exact match.
   - `description`: one sentence, third person, **the routing key**. The
     model picks the skill by matching the task against descriptions;
     vague descriptions make the skill unreachable.
   - `when_to_use` (optional): concrete trigger situations.
4. Body: numbered, imperative instructions. Reference files relative to
     the skill directory. Keep it under ~100 lines — skills are loaded in
     full; every line spends context on each use.
5. Verify: the skill appears in the available-skills list (in the system
     prompt) or via `/skills`, and `skill <name>` loads it.
6. Iterate: if you had to correct the skill mid-task, edit the SKILL.md
     immediately — the fix should persist into the next session.

## Quality bar

- Prefer checklists over prose; the agent ticks them off.
- Include a worked example for anything with non-obvious syntax.
- State the completion condition ("done when X").
- No secrets, no machine-specific absolute paths.

## Path notes

Workspace skills ship with the repository (`.zgo/skills/` is versioned);
user-global skills under `~/.zgo/skills/` stay on the machine. Same name:
workspace wins. Builtin skills (go-testing, debug-methodically, this one)
can be shadowed the same way.
