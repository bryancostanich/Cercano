---
name: compute-before-simulate
description: Compute the expected result before running any simulation or sweep.
---

# Compute Before Simulate

You are now operating under Dave's simulation protocol. The simulation verifies your computation — it does not replace it.

This exists because agents will reliably iterate blindly in simulation tools instead of computing analytically first. An agent spent 272 SPICE iterations finding a folded-cascode bias point that has a closed-form equation. This is not unique to hardware — agents do the same thing with any simulation or modeling tool.

## The Protocol

### 1. Identify What You're Determining

State the specific quantity or behavior you're trying to find. Not "see if it works" — what specific value or property?

### 2. Find the Governing Equations

The equations exist. For well-understood systems, the analytical solution is known. Look it up.

- Circuit design: Razavi, Allen & Holberg, Gray & Meyer
- Structural: beam equations, stress formulas
- Thermal: heat transfer coefficients, thermal resistance
- Performance: Little's law, queueing theory, Amdahl's law
- ML: convergence bounds, learning rate schedules

If you're working in a domain where closed-form solutions exist and you're not using them, you're brute-forcing.

### 3. Compute the Expected Result

With actual parameters, not placeholders. Write down:
- The equation you're using
- The values you're plugging in
- The expected result

### 4. Run ONE Simulation

One. To verify your computation.

### 5. Evaluate

- **Within ~20% of prediction**: Fine-tune with 1-3 more runs. You understood the system.
- **Wildly off**: The equations are wrong, or you used wrong parameters. Go back to step 2. Do NOT iterate in the simulator to "find" the right answer.

### 6. Hard Limits

- Maximum 3-5 simulation runs for initial design verification
- If you're past 5 runs, STOP. You're brute-forcing.
- Parameter sweeps are for characterizing a FINISHED design, not for finding the design

## What This Prevents

- Parameter sweeps to "find" a value that has a closed-form equation
- "Let me just try X and see what happens" — that's guessing, not engineering
- Iterating 50+ times when 3 runs should suffice
- Using simulation as a design tool instead of a verification tool
- Skipping the math because "the simulator will figure it out"

## The Test

Before running any simulation, you should be able to answer:
1. What specific result do I expect?
2. What equation produced that expectation?
3. If the simulation disagrees, which do I trust and why?

If you can't answer these, you don't understand the system well enough to simulate it.

