// ── Sketch node type definitions ──

export type ToolType = "pen" | "eraser" | "line" | "rect" | "circle" | "arrow" | "text" | "pose";

export interface FreehandStroke {
  tool: "pen" | "eraser";
  points: Array<[number, number]>;
  color: string;
  width: number;
}

export interface ShapeStroke {
  tool: "line" | "rect" | "circle" | "arrow";
  start: [number, number];
  end: [number, number];
  color: string;
  width: number;
}

export interface TextStroke {
  tool: "text";
  position: [number, number];
  text: string;
  color: string;
  width: number;
}

export type Stroke = FreehandStroke | ShapeStroke | TextStroke;

// ── Pose preset ids (shared by draggable pose layer below) ──

export type PoseKey = "stand" | "walk" | "cheer" | "sit" | "run" | "jump";

// ── Draggable-pose overlay (OpenPose-style 14 keypoints) ──

export type JointId =
  | "nose" | "neck"
  | "rShoulder" | "rElbow" | "rWrist"
  | "lShoulder" | "lElbow" | "lWrist"
  | "rHip" | "rKnee" | "rAnkle"
  | "lHip" | "lKnee" | "lAnkle";

export type Keypoint = [number, number];
export type PoseLayer = Record<JointId, Keypoint>;
export type PoseHandle = { poseIndex: number; jointId: JointId };