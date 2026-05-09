import { create } from "zustand";
import type { RPCClient } from "./client";

interface RpcStore {
  rpc: RPCClient | null;
  setRpc: (rpc: RPCClient) => void;
}

/** Global RPC client store — allows canvas nodes to call backend without prop drilling. */
export const useRpcStore = create<RpcStore>((set) => ({
  rpc: null,
  setRpc: (rpc) => set({ rpc }),
}));
