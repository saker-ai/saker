import { autoLayoutGraph } from "./layout";
import { useCanvasStore } from "./store";

export function autoLayoutCanvasAfterGeneration() {
  const store = useCanvasStore.getState();
  if (store.nodes.length === 0) return;

  const laidOut = autoLayoutGraph(store.nodes, store.edges);
  store.setNodes(laidOut);
  store.triggerFitView();
}
