#!/bin/sh
# gen_videos.sh — Generate deterministic test videos using ffmpeg.
# These synthetic videos have known content so e2e tests can verify
# that the analysis pipeline produces correct descriptions.
set -eu

OUT="${1:-fixtures/videos}"
mkdir -p "$OUT"

echo "Generating test videos in $OUT ..."

# Detect available encoder: prefer libx264, fallback to mpeg4
if ffmpeg -hide_banner -encoders 2>/dev/null | grep -q libx264; then
  VCODEC="libx264"
  EXTRA="-pix_fmt yuv420p"
else
  VCODEC="mpeg4"
  EXTRA=""
fi
echo "Using video codec: $VCODEC"

# 1. Color bars (5s) — smoke test for basic video analysis
ffmpeg -y -f lavfi -i "testsrc2=duration=5:size=640x480:rate=10" \
  -c:v "$VCODEC" $EXTRA -an \
  "$OUT/color_bars.mp4"

# 2. Countdown with timestamp text (10s) — text recognition + timeline
ffmpeg -y -f lavfi -i "color=c=black:s=640x480:d=10:r=5" \
  -vf "drawtext=text='%{pts\:hms}':fontsize=72:fontcolor=white:x=(w-tw)/2:y=(h-th)/2,\
drawtext=text='TEST VIDEO':fontsize=36:fontcolor=yellow:x=(w-tw)/2:y=40" \
  -c:v "$VCODEC" $EXTRA -an \
  "$OUT/countdown.mp4"

# 3. Scene change (60s, 3 scenes) — deep analysis with multiple segments
ffmpeg -y -f lavfi -i "\
color=c=red:s=640x480:d=20:r=10,drawtext=text='Scene 1 - Red':fontsize=48:fontcolor=white:x=20:y=20[v1];\
color=c=blue:s=640x480:d=20:r=10,drawtext=text='Scene 2 - Blue':fontsize=48:fontcolor=white:x=20:y=20[v2];\
color=c=green:s=640x480:d=20:r=10,drawtext=text='Scene 3 - Green':fontsize=48:fontcolor=white:x=20:y=20[v3];\
[v1][v2][v3]concat=n=3:v=1:a=0" \
  -c:v "$VCODEC" $EXTRA -an \
  "$OUT/scene_change.mp4"

# 4. Moving object (5s) — action detection
ffmpeg -y -f lavfi -i "color=c=white:s=640x480:d=5:r=10" \
  -vf "drawtext=text='MOVING':fontsize=60:fontcolor=red:x='mod(n*8,640)':y=200" \
  -c:v "$VCODEC" $EXTRA -an \
  "$OUT/moving_object.mp4"

echo "Done. Generated $(ls "$OUT"/*.mp4 | wc -l) test videos."
ls -lh "$OUT"/*.mp4
