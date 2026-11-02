---
name: commit-message
description: Use when writing or reviewing a pull request title/description or commit message, including when explaining a diff.
---

# Commit / PR Message

Pull requests are merged with a merge commit, and the repository is configured so the PR title and description become that merge commit's subject and body. Write the PR text as the final merge commit message. Commits on the branch are kept in history too, so give each a clear `<module>: <imperative summary>` subject, but the PR text is what readers of `git log --first-parent` see. When committing directly, use the same shape.

## Know the reader

A commit message is an invaluable reference during code review, development, and debugging. It is not a verbal rendering of the diff: it is the first thing a reader sees when asking "should I care about this?", so the title and the _why the change was made_ must answer that quickly. The code already shows what changed; only the message can say why. Answer the why directly rather than linking to a ticket or doc.

- **The reviewer now** (teammate or agent) has the diff open. They need the why, to judge whether the code does what it should: the problem, the decision, what was deliberately left out, and anything they'd otherwise have to ask about.
- **The maintainer in two years** is scanning `git log` and `git blame` while debugging. They need the symptom or requirement behind the change, and any operational requirement it introduced, to decide whether this commit matters to them.

Follow `.github/pull_request_template.md` for headings.

## Shape

```
<module>: <imperative summary, no period>

## What changed and why
<Why in one short sentence. Then what changed, at behaviour level.>

## Automated testing
<One honest line.>
```

## Explaining a diff

You are usually explaining a change you already have context for, from an issue, a conversation, or your own exploration. The message explains the diff to a reader; it does not retell how the change was developed.

**Sources**

- **The diff** is the primary source and the only authority on what changed. Read all of it and find the one problem it solves.
- **Issues, agent context, and exploration** put the change, and the reason for it, in context. That is all they are for: use them to explain why, not to add scope. Leave out a plan, investigation trails, and abandoned attempts.
- **If the diff is all you have**, infer the why from the symptom or the consumer (what a user, operator or CI job saw go wrong, or couldn't do), not from the code mechanism. If the why can't be known, ask the author.
- Never invent tickets, links, deploy steps, test runs or user scenarios, and never claim manual testing nobody did.

**What to leave in or out**

- Skip incidental changes (dropped debug logs, fixture renames, new parameters, lint overrides) unless a future reader could be bitten. Ignore generated files (codegen output, lockfiles, mocks).
- Operational requirements are not incidental: a new environment variable, credential, permission, tool or external system gets one sentence each.
- Common shapes and their usual why: logic extracted into a package means "so it can be unit tested"; dependency removed means unneeded or unmaintained; limit raised means something exceeded it; check or job deleted means it never worked, is ignored, or isn't needed; new endpoint plus new credentials means data exposed to callers who can't reach the store directly.

## Subject

- Lowercase `<module>:` prefix, using the most specific name people say aloud (the package, component or tool, e.g. `attehcl:`, `detector:`, `nix:`, `ci:`, `docs:`). Join several with commas. `Revert "..."` needs no prefix.
- Imperative verb (`fix`, `add`, `remove`, `use`, `allow`, `migrate`), never `adding`/`added`/`fixes`.
- Typically 50-65 characters; stay under 72. One clause: no commas, semicolons, em-dashes or "and also". Extra clauses go in the body.
- No trailing period, no `chore:`/`feat:`/`fix:` prefixes, no ticket ids, no PR number. The module name and imperative subject say enough, and keeping the title short helps scanning.
- Name the effect, not the mechanism: `attegit: fix temp dir paths on macos`, not `attegit: call EvalSymlinks on the temp root`.
- A searchable name (flag, env var, command, config key) someone would grep for belongs in the subject: `cmd/run: add --dry-run flag`.
- If the diff does two small things, lead with the one a reader would search for.

Bad → better: `Update readme.md` → `docs: correct install steps`; `adding hcl filename config` → `attehcl: enable configurable hcl filename per repository`; `PROJ-123 - move flag parsing` → `cmd: move flag parsing into root command`.

## Body

1. **Lead with why**, one short sentence (under ~25 words): symptom, situation or requirement, in that order of preference. E.g. "This check was never working and has been ignored for a while, removing it." / "Extracting the logic so we can write unit tests for it."
2. **Then what changed** in one or two sentences at behaviour level. A colleague who has never read the code should understand it. A list is fine if at most three one-line items.
3. **Length: typically 30-70 words.** Past roughly 120 you are almost certainly narrating the diff: cut, don't trim. Delete sentences about fallbacks, validation rules and identifiers first.
4. **No mechanism inventory**: versions, checksums, stdlib calls, signature changes, flags, fixture names, API field names, type names. Name something only if the reader needs the exact name to find it (a command, header, or table).
5. **One change, one message.** A third "Also..." means the extras are incidental (delete) or the PR should be split.
6. **Keep links that back a claim** (benchmark, bug report, prior PR) inline on that sentence, which must still make sense if the link is dead. No bare links in place of sentences. Never invent a link.
7. **Name deliberate omissions and follow-ups** in one sentence: "More systemic fix will follow, but this unblocks the release for now."
8. **Reference related PRs** as `#1234`. Don't state that the PR is part of a stack ("Stack 1/3", "Stacked on #42"); the tooling makes that clear. Referencing another PR in the stack is fine when it helps explain the why or a follow-up. Describe only this PR.
9. **Never use "I".** The message states what happened and why, not who did it. Write "Removes the check" or "This check never worked", not "I removed the check".
10. **Automated Testing: one honest line.** "Added tests." "Updated request tests to check for the header." "None, docs only." Never an empty heading, "for X to test", or reassurance. Do not mention what was run locally as part of the session: automated tests are re-run via continuous integration.
11. **Headings**: use only those you fill. Delete empty `Manual testing (optional)` and `Update documentation (optional)`. Fill Manual testing only with something real, including why it's impossible. No placeholder text or unedited template content: omitting a section should be a deliberate choice, not an accident.
12. **Trivial changes** (typo, docs, one-line config bump) need no body. Small configuration or debugging changes can omit testing.
13. **The message is not a verification report.** Pass counts, lint and type-check results, per-test enumerations, acceptance-criteria walkthroughs and rulings belong in a PR comment (it isn't merged) or the issue. The testing section stays one honest line.
14. **Do keep notes a reviewer or future maintainer can't get elsewhere**: "safe to merge without #1234", merge-order or dependency on other work, a bug found along the way and fixed here, and new operational requirements.
15. **Plain prose.** No bold, em-dashes, or "Acceptance criteria"/"Risks" sections.
16. **Consolidating sections is fine.** The template's headings are a guide; the information matters more than the structure.

## Example

```
attehcl: enable configurable hcl filename per repository

## What changed and why

Repositories could only use the default HCL filename, which clashes with
repositories that already use that name for something else. The filename
can now be set per repository.

## Automated testing

Added tests loading configuration from a custom filename.
```

Before finishing, reread once as the reviewer and once as the future maintainer, and delete sentences neither would miss.
