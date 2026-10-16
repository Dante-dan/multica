// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { ApiClient } from "../api/client";
import { setApiInstance } from "../api";
import { setCurrentWorkspace } from "../platform/workspace-storage";
import { pinKeys, pinListOptions } from "./queries";
import type { PinnedItem } from "../types";

const pin: PinnedItem = {
  id: "pin-a", workspace_id: "ws-a", user_id: "user", item_type: "issue",
  item_id: "issue-a", position: 1, created_at: "2026-10-16T00:00:00Z",
};
afterEach(() => {
  setCurrentWorkspace(null, null);
  vi.unstubAllGlobals();
});

it("keeps a delayed A response in A's cache after navigating to B", async () => {
  let resolveA!: (response: Response) => void;
  const fetchMock = vi.fn().mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveA = resolve; }))
    .mockResolvedValueOnce(Response.json([]));
  vi.stubGlobal("fetch", fetchMock);
  setApiInstance(new ApiClient("https://api.example.test"));
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  setCurrentWorkspace("b", "ws-b");
  const requestA = qc.fetchQuery(pinListOptions("ws-a", "user"));
  await qc.fetchQuery(pinListOptions("ws-b", "user"));
  expect(fetchMock.mock.calls[0]?.[1]?.headers).toMatchObject({ "X-Workspace-Slug": "", "X-Workspace-ID": "ws-a" });
  resolveA(Response.json([pin]));
  await requestA;
  expect(qc.getQueryData(pinKeys.list("ws-a", "user"))).toEqual([pin]);
  expect(qc.getQueryData(pinKeys.list("ws-b", "user"))).toEqual([]);
  expect(fetchMock.mock.calls.every(([, init]) => !init.method || init.method === "GET")).toBe(true);
  qc.clear();
});

it("rejects a response from a different workspace instead of caching it", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(Response.json([pin])));
  setApiInstance(new ApiClient("https://api.example.test"));
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await expect(qc.fetchQuery(pinListOptions("ws-b", "user"))).rejects.toThrow("different workspace");
  expect(qc.getQueryData(pinKeys.list("ws-b", "user"))).toBeUndefined();
  qc.clear();
});

it("binds create/delete/reorder to the explicit workspace after navigation", async () => {
  const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(Response.json(pin)));
  vi.stubGlobal("fetch", fetchMock);
  const client = new ApiClient("https://api.example.test");
  setCurrentWorkspace("b", "ws-b");
  await client.createPin({ item_type: "issue", item_id: "issue-a" }, "ws-a");
  await client.deletePin("issue", "issue-a", "ws-a");
  await client.reorderPins({ items: [{ id: "pin-a", position: 1 }] }, "ws-a");
  for (const [, init] of fetchMock.mock.calls) {
    expect(init.headers).toMatchObject({ "X-Workspace-Slug": "", "X-Workspace-ID": "ws-a" });
  }
});
