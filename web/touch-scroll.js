const DOM_DELTA_LINE = 1;
const DOM_DELTA_PAGE = 2;

// Release velocity uses only the last moments of a drag, like native scrolling.
const VELOCITY_WINDOW_MS = 100;
// A finger that rested this long before lifting ends without momentum.
const RELEASE_STALL_MS = 50;
// Samples closer together than one frame would otherwise report absurd speeds.
const MIN_VELOCITY_SPAN_MS = 16;
const MAX_FLING_VELOCITY = 8;
export const MIN_FLING_VELOCITY = 0.2;
export const MIN_MOMENTUM_VELOCITY = 0.02;
// iOS's normal scroll-view deceleration: velocity keeps 99.8% per millisecond.
const MOMENTUM_DECAY_PER_MS = 0.998;

export function wheelEventPixels(event, rowHeight, rows) {
  if (event.deltaMode === DOM_DELTA_LINE) {
    return event.deltaY * rowHeight;
  }
  if (event.deltaMode === DOM_DELTA_PAGE) {
    return event.deltaY * rowHeight * rows;
  }
  return event.deltaY;
}

// Converts scroll distance into whole application scroll steps, carrying the
// remainder so slow movement still adds up and reversals cancel it out.
export function createScrollStepper() {
  let remainder = 0;
  return {
    take(pixels, pixelsPerStep) {
      remainder += pixels;
      const steps = Math.trunc(remainder / pixelsPerStep);
      remainder -= steps * pixelsPerStep;
      return steps;
    },
    reset() {
      remainder = 0;
    },
  };
}

// Tracks a vertical drag. Velocity is in scroll pixels per millisecond and is
// positive when the finger moves up, matching a wheel event's deltaY.
export function createVelocityTracker() {
  let samples = [];
  return {
    reset(time, position) {
      samples = [{ time, position }];
    },
    add(time, position) {
      samples.push({ time, position });
      while (
        samples.length > 2 &&
        time - samples[0].time > VELOCITY_WINDOW_MS
      ) {
        samples.shift();
      }
    },
    releaseVelocity(time) {
      const recent = samples.filter(
        (sample) => time - sample.time <= VELOCITY_WINDOW_MS,
      );
      if (recent.length < 2 || time - recent.at(-1).time > RELEASE_STALL_MS) {
        return 0;
      }
      const first = recent[0];
      const last = recent.at(-1);
      const elapsed = Math.max(last.time - first.time, MIN_VELOCITY_SPAN_MS);
      const velocity = (first.position - last.position) / elapsed;
      return Math.max(
        -MAX_FLING_VELOCITY,
        Math.min(MAX_FLING_VELOCITY, velocity),
      );
    },
  };
}

// Advances an exponentially decaying fling. The distance is the exact
// integral over the interval, so the total travel is frame-rate independent.
export function momentumStep(velocity, elapsedMs) {
  const decay = MOMENTUM_DECAY_PER_MS ** elapsedMs;
  return {
    distance: (velocity * (1 - decay)) / -Math.log(MOMENTUM_DECAY_PER_MS),
    velocity: velocity * decay,
  };
}
