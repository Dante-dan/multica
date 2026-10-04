// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { runtimeLocalSkillsOptions, resolveRuntimeLocalSkills } from "./local-skills";

const initiate = vi.hoisted(() => vi.fn());
vi.mock("../api", () => ({ api: { initiateListLocalSkills: initiate } }));

describe("agent-scoped inherited skill discovery", () => {
  it("separates agents and profile revisions while preserving unscoped imports", () => {
    const admin = runtimeLocalSkillsOptions("runtime", "admin", "revision-1");
    const research = runtimeLocalSkillsOptions("runtime", "research", "revision-1");
    const changed = runtimeLocalSkillsOptions("runtime", "admin", "revision-2");
    expect(admin.queryKey).not.toEqual(research.queryKey);
    expect(admin.queryKey).not.toEqual(changed.queryKey);
    expect(runtimeLocalSkillsOptions("runtime").queryKey).toEqual(["runtimes", "local-skills", "runtime"]);
  });

  it("sends the selected agent to discovery instead of requesting a runtime-wide catalog", async () => {
    initiate.mockResolvedValue({ status: "completed", supported: true, skills: [{ key: "admin-only" }] });
    const result = await resolveRuntimeLocalSkills("shared-runtime", "admin");
    expect(initiate).toHaveBeenCalledWith("shared-runtime", "admin");
    expect(result.skills).toEqual([{ key: "admin-only" }]);
  });
});
