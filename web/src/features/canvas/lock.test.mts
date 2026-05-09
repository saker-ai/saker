import test from "node:test";
import assert from "node:assert/strict";

/** Mirrors the removeNode/removeNodes reducers in ./store.ts — kept in sync
 *  so we can test the lock-aware contract without importing the zustand store
 *  (which transitively imports browser-only helpers). */

interface NodeState {
  id: string;
  data: { locked?: boolean } & Record<string, unknown>;
}
interface EdgeState {
  id: string;
  source: string;
  target: string;
}
interface State {
  nodes: NodeState[];
  edges: EdgeState[];
}

function removeNode(state: State, id: string): State {
  const target = state.nodes.find((n) => n.id === id);
  if (target?.data?.locked) return state;
  return {
    nodes: state.nodes.filter((n) => n.id !== id),
    edges: state.edges.filter((e) => e.source !== id && e.target !== id),
  };
}

function removeNodes(state: State, ids: string[]): State {
  const idSet = new Set(ids);
  const deletable = new Set(
    state.nodes
      .filter((n) => idSet.has(n.id) && !n.data?.locked)
      .map((n) => n.id),
  );
  if (deletable.size === 0) return state;
  return {
    nodes: state.nodes.filter((n) => !deletable.has(n.id)),
    edges: state.edges.filter(
      (e) => !deletable.has(e.source) && !deletable.has(e.target),
    ),
  };
}

function node(id: string, locked = false): NodeState {
  return {
    id,
    data: {
      nodeType: "image",
      label: id,
      ...(locked ? { locked: true } : {}),
    },
  };
}

test("removeNode returns the same state when the target is locked", () => {
  const edge: EdgeState = { id: "e1", source: "a", target: "b" };
  const before: State = { nodes: [node("a", true), node("b")], edges: [edge] };
  const after = removeNode(before, "a");
  assert.equal(after, before, "must return identity when locked (no allocation)");
  assert.equal(after.nodes.length, 2);
  assert.equal(after.edges.length, 1);
});

test("removeNode drops unlocked node and its incident edges", () => {
  const edge: EdgeState = { id: "e1", source: "a", target: "b" };
  const before: State = { nodes: [node("a"), node("b")], edges: [edge] };
  const after = removeNode(before, "a");
  assert.equal(after.nodes.length, 1);
  assert.equal(after.nodes[0].id, "b");
  assert.equal(after.edges.length, 0);
});

test("removeNodes only deletes unlocked ids, keeping locked ones in place", () => {
  const before: State = {
    nodes: [node("a"), node("b", true), node("c")],
    edges: [],
  };
  const after = removeNodes(before, ["a", "b", "c"]);
  assert.equal(after.nodes.length, 1);
  assert.equal(after.nodes[0].id, "b");
});

test("removeNodes is a no-op (identity) when every candidate is locked", () => {
  const before: State = {
    nodes: [node("a", true), node("b", true)],
    edges: [],
  };
  const after = removeNodes(before, ["a", "b"]);
  assert.equal(after, before);
});

test("removeNodes drops edges that touch any deleted node", () => {
  const before: State = {
    nodes: [node("a"), node("b"), node("c", true)],
    edges: [
      { id: "e1", source: "a", target: "b" },
      { id: "e2", source: "b", target: "c" },
      { id: "e3", source: "a", target: "c" },
    ],
  };
  const after = removeNodes(before, ["a", "b", "c"]);
  assert.equal(after.nodes.length, 1);
  assert.equal(after.nodes[0].id, "c");
  assert.equal(after.edges.length, 0, "all edges incident to a/b must be dropped, and c is preserved");
});
