// ── Pose constants, utility functions, and drawing ──

import type { PoseKey, JointId, PoseLayer, PoseHandle } from "./SketchNode.types";

// ── Pose keys ──

export const POSE_KEYS: PoseKey[] = ["stand", "walk", "cheer", "sit", "run", "jump"];

// ── Joint ids ──

export const JOINT_IDS: JointId[] = [
  "nose", "neck",
  "rShoulder", "rElbow", "rWrist",
  "lShoulder", "lElbow", "lWrist",
  "rHip", "rKnee", "rAnkle",
  "lHip", "lKnee", "lAnkle",
];

// ── Bone connections (OpenPose color convention) ──

export const BONES: Array<{ a: JointId; b: JointId; color: string }> = [
  { a: "nose", b: "neck", color: "#ff0000" },
  { a: "neck", b: "rShoulder", color: "#ff5500" },
  { a: "rShoulder", b: "rElbow", color: "#ff9900" },
  { a: "rElbow", b: "rWrist", color: "#ffdd00" },
  { a: "neck", b: "lShoulder", color: "#aaff00" },
  { a: "lShoulder", b: "lElbow", color: "#55ff00" },
  { a: "lElbow", b: "lWrist", color: "#00ff22" },
  { a: "neck", b: "rHip", color: "#00ff88" },
  { a: "rHip", b: "rKnee", color: "#00ffff" },
  { a: "rKnee", b: "rAnkle", color: "#0088ff" },
  { a: "neck", b: "lHip", color: "#0022ff" },
  { a: "lHip", b: "lKnee", color: "#5500ff" },
  { a: "lKnee", b: "lAnkle", color: "#aa00ff" },
];

export const POSE_JOINT_R = 5;
export const POSE_HIT_R = 12;
export const POSE_BONE_WIDTH = 4;
const POSE_BODY_HIT_R = 10;

// ── Pose layer presets ──

export const POSE_LAYER_PRESETS: Record<PoseKey, PoseLayer> = {
  stand: {
    nose: [200, 40], neck: [200, 75],
    rShoulder: [175, 80], rElbow: [165, 125], rWrist: [160, 170],
    lShoulder: [225, 80], lElbow: [235, 125], lWrist: [240, 170],
    rHip: [185, 165], rKnee: [180, 215], rAnkle: [175, 265],
    lHip: [215, 165], lKnee: [220, 215], lAnkle: [225, 265],
  },
  walk: {
    nose: [200, 40], neck: [200, 75],
    rShoulder: [178, 80], rElbow: [200, 120], rWrist: [225, 155],
    lShoulder: [222, 80], lElbow: [195, 120], lWrist: [165, 150],
    rHip: [190, 165], rKnee: [225, 210], rAnkle: [245, 265],
    lHip: [210, 165], lKnee: [185, 210], lAnkle: [170, 265],
  },
  cheer: {
    nose: [200, 60], neck: [200, 90],
    rShoulder: [175, 95], rElbow: [155, 55], rWrist: [140, 15],
    lShoulder: [225, 95], lElbow: [245, 55], lWrist: [260, 15],
    rHip: [185, 180], rKnee: [178, 230], rAnkle: [172, 278],
    lHip: [215, 180], lKnee: [222, 230], lAnkle: [228, 278],
  },
  sit: {
    nose: [200, 55], neck: [200, 90],
    rShoulder: [175, 95], rElbow: [165, 135], rWrist: [158, 178],
    lShoulder: [225, 95], lElbow: [235, 135], lWrist: [242, 178],
    rHip: [190, 170], rKnee: [265, 175], rAnkle: [275, 245],
    lHip: [210, 170], lKnee: [255, 190], lAnkle: [245, 250],
  },
  run: {
    nose: [200, 45], neck: [205, 80],
    rShoulder: [185, 85], rElbow: [155, 110], rWrist: [130, 145],
    lShoulder: [225, 85], lElbow: [260, 85], lWrist: [285, 60],
    rHip: [195, 170], rKnee: [230, 210], rAnkle: [265, 250],
    lHip: [215, 170], lKnee: [170, 205], lAnkle: [150, 165],
  },
  jump: {
    nose: [200, 50], neck: [200, 85],
    rShoulder: [178, 90], rElbow: [155, 55], rWrist: [135, 20],
    lShoulder: [222, 90], lElbow: [245, 55], lWrist: [265, 20],
    rHip: [188, 170], rKnee: [172, 205], rAnkle: [180, 245],
    lHip: [212, 170], lKnee: [228, 205], lAnkle: [220, 245],
  },
};

// ── Pose utility functions ──

export function clonePoseLayer(p: PoseLayer): PoseLayer {
  const out = {} as PoseLayer;
  for (const id of JOINT_IDS) out[id] = [p[id][0], p[id][1]];
  return out;
}

export function normalizePoses(arr?: Array<Record<string, [number, number]>>): PoseLayer[] {
  if (!arr || arr.length === 0) return [];
  return arr.map((kp) => {
    const base = clonePoseLayer(POSE_LAYER_PRESETS.stand);
    for (const id of JOINT_IDS) {
      const v = kp[id];
      if (Array.isArray(v) && v.length === 2 && isFinite(v[0]) && isFinite(v[1])) {
        base[id] = [v[0], v[1]];
      }
    }
    return base;
  });
}

export function flattenPoses(poses: PoseLayer[]): Array<Record<string, [number, number]>> {
  return poses.map((pose) => {
    const flat: Record<string, [number, number]> = {};
    for (const id of JOINT_IDS) flat[id] = [pose[id][0], pose[id][1]];
    return flat;
  });
}

export function hitTestPoses(poses: PoseLayer[], x: number, y: number): PoseHandle | null {
  let best: { handle: PoseHandle; d: number } | null = null;
  for (let pi = 0; pi < poses.length; pi++) {
    const pose = poses[pi];
    for (const id of JOINT_IDS) {
      const [jx, jy] = pose[id];
      const d = Math.hypot(x - jx, y - jy);
      if (d <= POSE_HIT_R && (!best || d < best.d)) {
        best = { handle: { poseIndex: pi, jointId: id }, d };
      }
    }
  }
  return best?.handle ?? null;
}

export function findPoseNearPoint(poses: PoseLayer[], x: number, y: number, radius = 30): number {
  let best: { idx: number; d: number } | null = null;
  for (let pi = 0; pi < poses.length; pi++) {
    const pose = poses[pi];
    for (const id of JOINT_IDS) {
      const [jx, jy] = pose[id];
      const d = Math.hypot(x - jx, y - jy);
      if (d <= radius && (!best || d < best.d)) best = { idx: pi, d };
    }
  }
  return best?.idx ?? -1;
}

export function distPointToSegment(
  px: number, py: number,
  x1: number, y1: number,
  x2: number, y2: number,
): number {
  const dx = x2 - x1;
  const dy = y2 - y1;
  if (dx === 0 && dy === 0) return Math.hypot(px - x1, py - y1);
  const t = Math.max(0, Math.min(1, ((px - x1) * dx + (py - y1) * dy) / (dx * dx + dy * dy)));
  return Math.hypot(px - (x1 + t * dx), py - (y1 + t * dy));
}

export function hitTestPoseBody(poses: PoseLayer[], x: number, y: number, threshold = POSE_BODY_HIT_R): number {
  let best: { idx: number; d: number } | null = null;
  for (let pi = 0; pi < poses.length; pi++) {
    const pose = poses[pi];
    for (const bone of BONES) {
      const [x1, y1] = pose[bone.a];
      const [x2, y2] = pose[bone.b];
      const d = distPointToSegment(x, y, x1, y1, x2, y2);
      if (d <= threshold && (!best || d < best.d)) {
        best = { idx: pi, d };
      }
    }
  }
  return best?.idx ?? -1;
}

export function sameHandle(a: PoseHandle | null, b: PoseHandle | null): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.poseIndex === b.poseIndex && a.jointId === b.jointId;
}

// ── Pose overlay drawing ──

export function drawPoseOverlay(
  ctx: CanvasRenderingContext2D,
  poses: PoseLayer[],
  showHandles: boolean,
  hover: PoseHandle | null,
) {
  if (poses.length === 0) return;
  ctx.save();
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.lineWidth = POSE_BONE_WIDTH;
  ctx.globalCompositeOperation = "source-over";
  for (const pose of poses) {
    for (const bone of BONES) {
      const [x1, y1] = pose[bone.a];
      const [x2, y2] = pose[bone.b];
      ctx.strokeStyle = bone.color;
      ctx.beginPath();
      ctx.moveTo(x1, y1);
      ctx.lineTo(x2, y2);
      ctx.stroke();
    }
  }
  if (showHandles) {
    for (let pi = 0; pi < poses.length; pi++) {
      const pose = poses[pi];
      for (const id of JOINT_IDS) {
        const [x, y] = pose[id];
        const isHover = !!hover && hover.poseIndex === pi && hover.jointId === id;
        ctx.beginPath();
        ctx.arc(x, y, isHover ? POSE_JOINT_R + 2 : POSE_JOINT_R, 0, Math.PI * 2);
        ctx.fillStyle = isHover ? "#ffffff" : "#ffdd00";
        ctx.fill();
        ctx.lineWidth = 1.5;
        ctx.strokeStyle = "#000000";
        ctx.stroke();
      }
    }
  }
  ctx.restore();
}