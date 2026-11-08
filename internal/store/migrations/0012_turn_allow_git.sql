-- allow_git belongs to one lead command: the turn it opened and that command's automatic fix turns (internal/task,
-- Turn.allowGit). Earlier versions kept it on the task, where it stayed on for every later turn. The flag moves to
-- the task's latest turn (the command in flight, or the last one, as before the upgrade) and leaves the task.
UPDATE turns SET data = json_set(data, '$.allowGit', json('true'))
WHERE task_id IN (SELECT id FROM tasks WHERE json_extract(data, '$.allowGit') = 1)
  AND n = (SELECT max(t2.n) FROM turns AS t2 WHERE t2.task_id = turns.task_id);
UPDATE tasks SET data = json_remove(data, '$.allowGit') WHERE json_extract(data, '$.allowGit') IS NOT NULL;
