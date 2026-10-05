# Native Panel Controls

A concise guide to native panel controls, distinguishing four things that are
often confused: receipts, delivery, checkpoints, and process exit.

## Sessions, agents and runs

- Queued follow-ups stay in the original Session.
- End-of-turn follow-ups queue in the original Session that produced them.
- Expand the current owned running Agent to issue further controls.
- Instructions attached to the current tool take effect after that tool
  completes, before the next model request is generated.

## Known ended and unknown Runs

- Known ended or unknown Runs remain readable, but they cannot accept new
  development controls.
- Unknown means execution identity or outcome cannot be confirmed; it does not
  mean the task is unlisted.

## Receipts do not prove delivery

Control receipts do not prove delivery. A protocol receipt only acknowledges
that a control was accepted; it does not prove resulting code delivery.

- Do not claim an exercise passed merely from a receipt.

## Pause acknowledgement is not a checkpoint

- A graceful pause acknowledgement does not prove a checkpoint exists.
- The Task must be paused with a verified checkpoint before explicit
  coordinator resume is allowed.

## Stop acceptance is not process exit

- Stop acceptance does not prove the process exited; verify the Run outcome.

## Outside scope

Other files, installation, credentials and remote actions are outside scope.
