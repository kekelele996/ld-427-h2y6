-- 支出申请被驳回后允许修改重提：保留驳回时的金额与理由快照供审批人对照，
-- 并记录驳回后修改重提的次数。
ALTER TABLE expense_records ADD COLUMN IF NOT EXISTS rejected_amount DOUBLE PRECISION;
ALTER TABLE expense_records ADD COLUMN IF NOT EXISTS rejection_comment VARCHAR(512) NOT NULL DEFAULT '';
ALTER TABLE expense_records ADD COLUMN IF NOT EXISTS revision_count INTEGER NOT NULL DEFAULT 0;
