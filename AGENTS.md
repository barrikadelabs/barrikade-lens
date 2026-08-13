## Verum
- Call brain_search before exploring code or changing architecture; if the verdict is ask_human, show the conflicts and stop.
- Call brain_lease before editing files other agents may touch; release it when done.
- Start long tasks with brain_task_start and checkpoint with its taskId.
- Finish tasks with brain_learn, then brain_task_complete.
