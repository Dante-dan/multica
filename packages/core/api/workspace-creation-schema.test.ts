// @vitest-environment node
import { describe, expect, it } from "vitest";
import { UserSchema } from "./schemas";

describe("workspace creation capability", () => {
  it("keeps explicit denial and allows absent older-server fields", () => {
    expect(UserSchema.parse({ id: "a", can_create_workspace: false }).can_create_workspace).toBe(false);
    expect(UserSchema.parse({ id: "a" }).can_create_workspace).toBeUndefined();
  });
  it("handles malformed additive capability fields", () => {
    expect(UserSchema.parse({ id: "a", can_create_workspace: "unexpected" }).can_create_workspace).toBe(true);
  });
});
