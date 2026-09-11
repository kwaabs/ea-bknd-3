-- Bug fix: internal/etl's RunStatusCancelled ("cancelled", added alongside
-- Engine.Cancel/the admin UI's Stop button) was never reflected here --
-- app.etl_job_runs.status still only allowed ('running', 'success',
-- 'failed'). finishRun writing status='cancelled' for a stopped run would
-- violate this CHECK constraint and fail, meaning Stop couldn't actually
-- record that it happened.
ALTER TABLE app.etl_job_runs DROP CONSTRAINT IF EXISTS etl_job_runs_status_check;
ALTER TABLE app.etl_job_runs ADD CONSTRAINT etl_job_runs_status_check
    CHECK (status IN ('running', 'success', 'failed', 'cancelled'));
