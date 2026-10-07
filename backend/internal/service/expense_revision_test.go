package service

import (
	"context"
	"errors"
	"testing"

	"github.com/renovation/renovation-budget-api/internal/constants"
	"github.com/renovation/renovation-budget-api/internal/dto"
	"github.com/renovation/renovation-budget-api/internal/model"
)

func setupRejectedExpense(t *testing.T, totalAmount, expenseAmount float64) (
	ctx context.Context,
	budgetRepo *fakeBudgetRepo,
	itemRepo *fakeItemRepo,
	expenseRepo *fakeExpenseRepo,
	svc *ExpenseService,
	sheet *model.BudgetSheet,
	item *model.BudgetItem,
	record *model.ExpenseRecord,
) {
	t.Helper()
	ctx = context.Background()
	audit := NewAuditService(newFakeAuditRepo(), testLogger())
	budgetRepo = newFakeBudgetRepo()
	itemRepo = newFakeItemRepo()
	expenseRepo = newFakeExpenseRepo()
	svc = NewExpenseService(expenseRepo, itemRepo, budgetRepo, audit, nil, testLogger())

	sheet, err := NewBudgetService(budgetRepo, itemRepo, audit, nil, testLogger()).Create(
		ctx, model.Actor{UserID: 1, Username: "project"},
		dto.CreateBudgetRequest{ProjectID: "p-1", Name: "整屋装修", TotalAmount: totalAmount})
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	item, err = NewItemService(itemRepo, budgetRepo, audit, nil, testLogger()).Create(
		ctx, model.Actor{UserID: 1}, sheet.ID,
		dto.CreateItemRequest{Category: constants.BudgetCategoryMaterial, BudgetAmount: totalAmount})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	record, err = svc.Create(ctx, model.Actor{UserID: 2, Username: "accountant"}, dto.CreateExpenseRequest{
		BudgetItemID:  item.ID,
		Amount:        expenseAmount,
		ExpenseDate:   "2026-08-01",
		PaymentMethod: constants.PaymentMethodBankTransfer,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := svc.Submit(ctx, model.Actor{UserID: 2}, record.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := svc.Reject(ctx, model.Actor{UserID: 3, Username: "finance"}, record.ID,
		dto.RejectExpenseRequest{ApprovalComment: "发票号缺失"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	record, _ = expenseRepo.FindByID(ctx, record.ID)
	return
}

func TestRejectedExpenseUpdateAndResubmit(t *testing.T) {
	ctx, budgetRepo, itemRepo, _, svc, sheet, item, record := setupRejectedExpense(t, 10000, 500)

	// 驳回后冻结额度已释放。
	budget, _ := budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 0 || budget.AvailableAmount != 10000 {
		t.Fatalf("after reject frozen=%v available=%v, want 0/10000", budget.FrozenAmount, budget.AvailableAmount)
	}
	if record.RejectedAmount == nil || *record.RejectedAmount != 500 {
		t.Fatalf("rejected_amount = %v, want 500", record.RejectedAmount)
	}
	if record.RejectionComment != "发票号缺失" {
		t.Fatalf("rejection_comment = %q, want 发票号缺失", record.RejectionComment)
	}

	// 在另一个预算项下新建预算项，验证可以改预算项。
	otherItem, err := NewItemService(itemRepo, budgetRepo, NewAuditService(newFakeAuditRepo(), testLogger()), nil, testLogger()).Create(
		ctx, model.Actor{UserID: 1}, sheet.ID,
		dto.CreateItemRequest{Category: constants.BudgetCategoryLabor, BudgetAmount: 10000})
	if err != nil {
		t.Fatalf("create other item: %v", err)
	}
	supplierID := uint(7)
	updated, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, record.ID, dto.UpdateRejectedExpenseRequest{
		BudgetItemID: otherItem.ID,
		Amount:       800,
		SupplierID:   &supplierID,
		InvoiceNo:    "INV-001",
		Description:  "补录发票号",
	})
	if err != nil {
		t.Fatalf("update rejected: %v", err)
	}
	if updated.Status != constants.ExpenseStatusRejected {
		t.Fatalf("status = %q, want Rejected（修改后仍待重新提交）", updated.Status)
	}
	if updated.BudgetItemID != otherItem.ID || updated.Amount != 800 || updated.InvoiceNo != "INV-001" || updated.Description != "补录发票号" {
		t.Fatalf("updated record mismatch: %+v", updated)
	}
	// 修改阶段不动预算冻结。
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 0 {
		t.Fatalf("frozen after update = %v, want 0", budget.FrozenAmount)
	}

	resubmitted, err := svc.Resubmit(ctx, model.Actor{UserID: 2}, record.ID)
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if resubmitted.Status != constants.ExpenseStatusSubmitted {
		t.Fatalf("status = %q, want Submitted", resubmitted.Status)
	}
	if resubmitted.RevisionCount != 1 {
		t.Fatalf("revision_count = %d, want 1", resubmitted.RevisionCount)
	}
	// 原金额与驳回理由仍留在记录上供审批人对照。
	if resubmitted.RejectedAmount == nil || *resubmitted.RejectedAmount != 500 {
		t.Fatalf("rejected_amount = %v, want 500 preserved", resubmitted.RejectedAmount)
	}
	if resubmitted.RejectionComment != "发票号缺失" {
		t.Fatalf("rejection_comment = %q, want preserved", resubmitted.RejectionComment)
	}
	// 预算表按新金额重新冻结。
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 800 || budget.AvailableAmount != 9200 {
		t.Fatalf("after resubmit frozen=%v available=%v, want 800/9200", budget.FrozenAmount, budget.AvailableAmount)
	}

	// 重提后审批通过：冻结转已支出，流程在同一条记录上继续往下走。
	approved, err := svc.Approve(ctx, model.Actor{UserID: 3}, record.ID, dto.ApproveExpenseRequest{ApprovalComment: "已补发票，同意"})
	if err != nil {
		t.Fatalf("approve after resubmit: %v", err)
	}
	if approved.Status != constants.ExpenseStatusApproved {
		t.Fatalf("status = %q, want Approved", approved.Status)
	}
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 0 || budget.SpentAmount != 800 || budget.AvailableAmount != 9200 {
		t.Fatalf("after approve frozen=%v spent=%v available=%v, want 0/800/9200", budget.FrozenAmount, budget.SpentAmount, budget.AvailableAmount)
	}
	_ = item
}

func TestResubmitInsufficientBalanceReverts(t *testing.T) {
	ctx, budgetRepo, _, expenseRepo, svc, sheet, _, record := setupRejectedExpense(t, 600, 500)

	// 另有一笔 200 元审批中的支出占住额度，可用余额仅剩 400。
	other, err := svc.Create(ctx, model.Actor{UserID: 2}, dto.CreateExpenseRequest{
		BudgetItemID:  record.BudgetItemID,
		Amount:        200,
		ExpenseDate:   "2026-08-02",
		PaymentMethod: constants.PaymentMethodCash,
	})
	if err != nil {
		t.Fatalf("create other expense: %v", err)
	}
	if _, err := svc.Submit(ctx, model.Actor{UserID: 2}, other.ID); err != nil {
		t.Fatalf("submit other: %v", err)
	}

	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, record.ID, dto.UpdateRejectedExpenseRequest{
		BudgetItemID: record.BudgetItemID,
		Amount:       500,
		InvoiceNo:    "INV-002",
	}); err != nil {
		t.Fatalf("update rejected: %v", err)
	}
	_, err = svc.Resubmit(ctx, model.Actor{UserID: 2}, record.ID)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("resubmit error = %v, want ErrInsufficientBalance", err)
	}
	// 整条退回：记录仍为 Rejected，修改内容保留，修订次数不增加。
	stored, _ := expenseRepo.FindByID(ctx, record.ID)
	if stored.Status != constants.ExpenseStatusRejected {
		t.Fatalf("status = %q, want Rejected", stored.Status)
	}
	if stored.Amount != 500 || stored.InvoiceNo != "INV-002" {
		t.Fatalf("edits should be kept on rejection: %+v", stored)
	}
	if stored.RevisionCount != 0 {
		t.Fatalf("revision_count = %d, want 0", stored.RevisionCount)
	}
	// 预算冻结维持在另一笔支出的 200，未被这次失败的提交改动。
	budget, _ := budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 200 || budget.AvailableAmount != 400 {
		t.Fatalf("after failed resubmit frozen=%v available=%v, want 200/400", budget.FrozenAmount, budget.AvailableAmount)
	}

	// 把金额改到余额以内后可以重新提交成功。
	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, record.ID, dto.UpdateRejectedExpenseRequest{
		BudgetItemID: record.BudgetItemID,
		Amount:       400,
		InvoiceNo:    "INV-002",
	}); err != nil {
		t.Fatalf("update rejected again: %v", err)
	}
	resubmitted, err := svc.Resubmit(ctx, model.Actor{UserID: 2}, record.ID)
	if err != nil {
		t.Fatalf("resubmit within balance: %v", err)
	}
	if resubmitted.Status != constants.ExpenseStatusSubmitted || resubmitted.RevisionCount != 1 {
		t.Fatalf("resubmitted = status %q revision %d", resubmitted.Status, resubmitted.RevisionCount)
	}
}

func TestMutateRejectedOnlyAllowedInRejected(t *testing.T) {
	ctx, _, _, _, svc, _, _, rejected := setupRejectedExpense(t, 10000, 500)
	editReq := dto.UpdateRejectedExpenseRequest{BudgetItemID: rejected.BudgetItemID, Amount: 100}

	// Rejected：修改合法，随后重提进入 Submitted。
	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, rejected.ID, editReq); err != nil {
		t.Fatalf("update rejected: %v", err)
	}
	submitted, err := svc.Resubmit(ctx, model.Actor{UserID: 2}, rejected.ID)
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}

	// Submitted：修改和重提都不允许。
	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, rejected.ID, editReq); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update submitted: err = %v, want ErrInvalidState", err)
	}
	if _, err := svc.Resubmit(ctx, model.Actor{UserID: 2}, rejected.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resubmit submitted: err = %v, want ErrInvalidState", err)
	}

	// Approved：仍不允许。
	if _, err := svc.Approve(ctx, model.Actor{UserID: 3}, rejected.ID, dto.ApproveExpenseRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, rejected.ID, editReq); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update approved: err = %v, want ErrInvalidState", err)
	}
	if _, err := svc.Resubmit(ctx, model.Actor{UserID: 2}, rejected.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resubmit approved: err = %v, want ErrInvalidState", err)
	}

	// Paid：仍不允许。
	if _, err := svc.Pay(ctx, model.Actor{UserID: 3}, rejected.ID, dto.PayExpenseRequest{}); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, rejected.ID, editReq); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update paid: err = %v, want ErrInvalidState", err)
	}
	if _, err := svc.Resubmit(ctx, model.Actor{UserID: 2}, rejected.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resubmit paid: err = %v, want ErrInvalidState", err)
	}
	_ = submitted
}

func TestUpdateRejectedUnknownBudgetItem(t *testing.T) {
	ctx, _, _, _, svc, _, _, record := setupRejectedExpense(t, 10000, 500)
	if _, err := svc.UpdateRejected(ctx, model.Actor{UserID: 2}, record.ID, dto.UpdateRejectedExpenseRequest{
		BudgetItemID: 999999,
		Amount:       100,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
