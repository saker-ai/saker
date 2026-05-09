import { useEffect, useRef } from "react";

interface Particle {
  x: number;
  y: number;
  vx: number;
  vy: number;
  r: number;
  baseR: number;
  opacity: number;
  phase: number;
  phaseSpeed: number;
}

export function ParticleBackground() {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let animId = 0;
    let particles: Particle[] = [];
    let mouse = { x: -1000, y: -1000 };
    const isMobile = window.innerWidth < 640;
    const LINK_DIST = isMobile ? 100 : 150;
    const MOUSE_DIST = isMobile ? 150 : 200;
    const COUNT = isMobile ? 40 : 80;

    function resize() {
      const dpr = window.devicePixelRatio || 1;
      const rect = canvas!.getBoundingClientRect();
      canvas!.width = rect.width * dpr;
      canvas!.height = rect.height * dpr;
      ctx!.setTransform(dpr, 0, 0, dpr, 0, 0);
    }

    function init() {
      resize();
      const w = canvas!.getBoundingClientRect().width;
      const h = canvas!.getBoundingClientRect().height;
      particles = Array.from({ length: COUNT }, () => {
        const baseR = Math.random() * 1.5 + 0.5;
        return {
          x: Math.random() * w,
          y: Math.random() * h,
          vx: (Math.random() - 0.5) * 0.35,
          vy: (Math.random() - 0.5) * 0.35,
          r: baseR,
          baseR,
          opacity: Math.random() * 0.5 + 0.3,
          phase: Math.random() * Math.PI * 2,
          phaseSpeed: Math.random() * 0.015 + 0.005,
        };
      });
    }

    function hexToRgb(hex: string) {
      const m = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
      // Fallback matches the default Dark theme accent (--accent: #fb7185 coral)
      // declared in 00-base.css. Used only when --accent is missing or non-hex.
      return m
        ? { r: parseInt(m[1], 16), g: parseInt(m[2], 16), b: parseInt(m[3], 16) }
        : { r: 251, g: 113, b: 133 };
    }

    function draw() {
      const w = canvas!.getBoundingClientRect().width;
      const h = canvas!.getBoundingClientRect().height;
      ctx!.clearRect(0, 0, w, h);

      const style = getComputedStyle(document.documentElement);
      const accent = style.getPropertyValue("--accent").trim() || "#fb7185";
      const rgb = hexToRgb(accent);

      for (const p of particles) {
        // Pulsing
        p.phase += p.phaseSpeed;
        const pulse = Math.sin(p.phase) * 0.3 + 1;
        p.r = p.baseR * pulse;

        // Mouse repulsion
        const mdx = p.x - mouse.x;
        const mdy = p.y - mouse.y;
        const mDist = Math.sqrt(mdx * mdx + mdy * mdy);
        if (mDist < MOUSE_DIST && mDist > 0) {
          const force = (1 - mDist / MOUSE_DIST) * 0.8;
          p.vx += (mdx / mDist) * force;
          p.vy += (mdy / mDist) * force;
        }

        // Keep particles always moving: decay toward base speed, never below
        const speed = Math.sqrt(p.vx * p.vx + p.vy * p.vy);
        const BASE = 0.18;
        if (speed > BASE) {
          // Decay excess from mouse push
          const target = BASE + (speed - BASE) * 0.95;
          p.vx = (p.vx / speed) * target;
          p.vy = (p.vy / speed) * target;
        } else if (speed > 0 && speed < BASE) {
          // Restore to base speed
          p.vx = (p.vx / speed) * BASE;
          p.vy = (p.vy / speed) * BASE;
        }

        p.x += p.vx;
        p.y += p.vy;
        if (p.x < 0) p.x = w;
        if (p.x > w) p.x = 0;
        if (p.y < 0) p.y = h;
        if (p.y > h) p.y = 0;

        // Glow
        const glow = ctx!.createRadialGradient(p.x, p.y, 0, p.x, p.y, p.r * 3);
        glow.addColorStop(0, `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${p.opacity * pulse})`);
        glow.addColorStop(1, `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, 0)`);
        ctx!.beginPath();
        ctx!.arc(p.x, p.y, p.r * 3, 0, Math.PI * 2);
        ctx!.fillStyle = glow;
        ctx!.globalAlpha = 1;
        ctx!.fill();

        // Core dot
        ctx!.beginPath();
        ctx!.arc(p.x, p.y, p.r, 0, Math.PI * 2);
        ctx!.fillStyle = accent;
        ctx!.globalAlpha = p.opacity + 0.2;
        ctx!.fill();
      }

      // Draw links
      for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
          const dx = particles[i].x - particles[j].x;
          const dy = particles[i].y - particles[j].y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          if (dist < LINK_DIST) {
            const alpha = (1 - dist / LINK_DIST) * 0.2;
            ctx!.beginPath();
            ctx!.moveTo(particles[i].x, particles[i].y);
            ctx!.lineTo(particles[j].x, particles[j].y);
            ctx!.strokeStyle = `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${alpha})`;
            ctx!.globalAlpha = 1;
            ctx!.lineWidth = (1 - dist / LINK_DIST) * 0.8;
            ctx!.stroke();
          }
        }
      }

      // Mouse connections
      for (const p of particles) {
        const dx = p.x - mouse.x;
        const dy = p.y - mouse.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        if (dist < MOUSE_DIST) {
          const alpha = (1 - dist / MOUSE_DIST) * 0.3;
          ctx!.beginPath();
          ctx!.moveTo(p.x, p.y);
          ctx!.lineTo(mouse.x, mouse.y);
          ctx!.strokeStyle = `rgba(${rgb.r}, ${rgb.g}, ${rgb.b}, ${alpha})`;
          ctx!.globalAlpha = 1;
          ctx!.lineWidth = (1 - dist / MOUSE_DIST) * 1.2;
          ctx!.stroke();
        }
      }

      ctx!.globalAlpha = 1;
      animId = requestAnimationFrame(draw);
    }

    init();
    draw();

    const onResize = () => resize();
    const onMouseMove = (e: MouseEvent) => {
      const rect = canvas!.getBoundingClientRect();
      mouse.x = e.clientX - rect.left;
      mouse.y = e.clientY - rect.top;
    };
    const onMouseLeave = () => { mouse.x = -1000; mouse.y = -1000; };
    const onTouchMove = (e: TouchEvent) => {
      const touch = e.touches[0];
      if (!touch) return;
      const rect = canvas!.getBoundingClientRect();
      mouse.x = touch.clientX - rect.left;
      mouse.y = touch.clientY - rect.top;
    };
    const onTouchEnd = () => { mouse.x = -1000; mouse.y = -1000; };

    window.addEventListener("resize", onResize);
    canvas.addEventListener("mousemove", onMouseMove);
    canvas.addEventListener("mouseleave", onMouseLeave);
    canvas.addEventListener("touchmove", onTouchMove, { passive: true });
    canvas.addEventListener("touchend", onTouchEnd);

    return () => {
      cancelAnimationFrame(animId);
      window.removeEventListener("resize", onResize);
      canvas.removeEventListener("mousemove", onMouseMove);
      canvas.removeEventListener("mouseleave", onMouseLeave);
      canvas.removeEventListener("touchmove", onTouchMove);
      canvas.removeEventListener("touchend", onTouchEnd);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      className="particle-bg"
      style={{ pointerEvents: "auto" }}
      aria-hidden="true"
    />
  );
}
