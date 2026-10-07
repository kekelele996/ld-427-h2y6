package service

import (
	"context"
	"errors"
	"testing"

	"github.com/renovation/renovation-budget-api/internal/constants"
	"github.com/renovation/renovation-budget-api/internal/dto"
	"github.com/renovation/renovation-budget-api/internal/model"
)

func TestExpenseServiceFullFlow(t *testing.T) {
	ctx := context.Background()
	audit := NewAuditService(newFakeAuditRepo(), testLogger())
	budgetRepo := newFakeBudgetRepo()
	itemRepo := newFakeItemRepo()
	budgetSvc := NewBudgetService(budgetRepo, itemRepo, audit, nil, testLogger())
	expenseSvc := NewExpenseService(newFakeExpenseRepo(), itemRepo, budgetRepo, audit, nil, testLogger())

	sheet, err := budgetSvc.Create(ctx, model.Actor{UserID: 1, Username: "project"}, dto.CreateBudgetRequest{ProjectID: "p-1", Name: "整屋装修", TotalAmount: 10000})
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}
	item, err := NewItemService(itemRepo, budgetRepo, audit, nil, testLogger()).Create(ctx, model.Actor{UserID: 1}, sheet.ID, dto.CreateItemRequest{Category: constants.BudgetCategoryMaterial, BudgetAmount: 5000})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	record, err := expenseSvc.Create(ctx, model.Actor{UserID: 2, Username: "accountant"}, dto.CreateExpenseRequest{
		BudgetItemID:  item.ID,
		Amount:        500,
		ExpenseDate:   "2026-08-01",
		PaymentMethod: constants.PaymentMethodBankTransfer,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if record.Status != constants.ExpenseStatusDraft {
		t.Fatalf("status = %q, want Draft", record.Status)
	}

	if _, err := expenseSvc.Submit(ctx, model.Actor{UserID: 2}, record.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	budget, _ := budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 500 || budget.AvailableAmount != 9500 {
		t.Fatalf("after submit frozen=%v available=%v", budget.FrozenAmount, budget.AvailableAmount)
	}

	if _, err := expenseSvc.Approve(ctx, model.Actor{UserID: 3, Username: "finance"}, record.ID, dto.ApproveExpenseRequest{ApprovalComment: "同意"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	updatedItem, _ := itemRepo.FindByID(ctx, item.ID)
	if budget.FrozenAmount != 0 || budget.SpentAmount != 500 || budget.AvailableAmount != 9500 {
		t.Fatalf("after approve frozen=%v spent=%v available=%v", budget.FrozenAmount, budget.SpentAmount, budget.AvailableAmount)
	}
	if updatedItem.SpentAmount != 500 || updatedItem.VarianceAmount != -4500 {
		t.Fatalf("item spent=%v variance=%v", updatedItem.SpentAmount, updatedItem.VarianceAmount)
	}

	paid, err := expenseSvc.Pay(ctx, model.Actor{UserID: 3}, record.ID, dto.PayExpenseRequest{PaymentDate: "2026-08-10"})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid.Status != constants.ExpenseStatusPaid {
		t.Fatalf("status = %q, want Paid", paid.Status)
	}
}

func TestExpenseServiceSubmitInsufficient(t *testing.T) {
	ctx := context.Background()
	audit := NewAuditService(newFakeAuditRepo(), testLogger())
	budgetRepo := newFakeBudgetRepo()
	itemRepo := newFakeItemRepo()
	expenseSvc := NewExpenseService(newFakeExpenseRepo(), itemRepo, budgetRepo, audit, nil, testLogger())

	sheet := &model.BudgetSheet{ProjectID: "p-1", Name: "预算", TotalAmount: 100, AvailableAmount: 100, Status: constants.BudgetStatusDraft, CreatedByID: 1, Version: 1}
	if err := budgetRepo.Create(ctx, sheet); err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	item := &model.BudgetItem{BudgetSheetID: sheet.ID, Category: constants.BudgetCategoryLabor, BudgetAmount: 100, VarianceAmount: -100}
	if err := itemRepo.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}
	record, err := expenseSvc.Create(ctx, model.Actor{UserID: 1}, dto.CreateExpenseRequest{BudgetItemID: item.ID, Amount: 200, ExpenseDate: "2026-08-01", PaymentMethod: constants.PaymentMethodCash})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := expenseSvc.Submit(ctx, model.Actor{UserID: 1}, record.ID); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("error = %v, want ErrInsufficientBalance", err)
	}
}

// TestExpenseServiceRejectEditResubmitFlow 验证驳回后在原记录上修改并重新提交的完整链路：
// 驳回时快照金额与理由并释放冻结，修改后重新提交按新金额重新冻结，审批人可对照快照。
func TestExpenseServiceRejectEditResubmitFlow(t *testing.T) {
	ctx := context.Background()
	audit := NewAuditService(newFakeAuditRepo(), testLogger())
	budgetRepo := newFakeBudgetRepo()
	itemRepo := newFakeItemRepo()
	expenseSvc := NewExpenseService(newFakeExpenseRepo(), itemRepo, budgetRepo, audit, nil, testLogger())

	sheet := &model.BudgetSheet{ProjectID: "p-1", Name: "预算", TotalAmount: 10000, AvailableAmount: 10000, Status: constants.BudgetStatusActive, CreatedByID: 1, Version: 1}
	if err := budgetRepo.Create(ctx, sheet); err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	item := &model.BudgetItem{BudgetSheetID: sheet.ID, Category: constants.BudgetCategoryMaterial, BudgetAmount: 5000, VarianceAmount: -5000}
	if err := itemRepo.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}
	item2 := &model.BudgetItem{BudgetSheetID: sheet.ID, Category: constants.BudgetCategoryLabor, BudgetAmount: 3000, VarianceAmount: -3000}
	if err := itemRepo.Create(ctx, item2); err != nil {
		t.Fatalf("create item2: %v", err)
	}

	applicant := model.Actor{UserID: 2, Username: "accountant"}
	record, err := expenseSvc.Create(ctx, applicant, dto.CreateExpenseRequest{
		BudgetItemID:  item.ID,
		Amount:        800,
		ExpenseDate:   "2026-08-01",
		PaymentMethod: constants.PaymentMethodBankTransfer,
		InvoiceNo:     "INV-001",
		Description:   "瓷砖款",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := expenseSvc.Submit(ctx, applicant, record.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}

	rejected, err := expenseSvc.Reject(ctx, model.Actor{UserID: 3, Username: "finance"}, record.ID, dto.RejectExpenseRequest{ApprovalComment: "发票号有误"})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.LastRejectedAmount == nil || *rejected.LastRejectedAmount != 800 || rejected.LastRejectedComment != "发票号有误" {
		t.Fatalf("last rejected snapshot = %v/%q", rejected.LastRejectedAmount, rejected.LastRejectedComment)
	}
	budget, _ := budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 0 || budget.AvailableAmount != 10000 {
		t.Fatalf("after reject frozen=%v available=%v", budget.FrozenAmount, budget.AvailableAmount)
	}

	// 申请人接着在原记录上修改预算项、金额、发票号和说明。
	supplierID := uint(9)
	updated, err := expenseSvc.Update(ctx, applicant, record.ID, dto.UpdateExpenseRequest{
		BudgetItemID:  item2.ID,
		Amount:        600,
		ExpenseDate:   "2026-08-02",
		PaymentMethod: constants.PaymentMethodCompany,
		SupplierID:    &supplierID,
		InvoiceNo:     "INV-001A",
		Description:   "瓷砖款（重开发票）",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != constants.ExpenseStatusRejected {
		t.Fatalf("status after update = %q, want Rejected", updated.Status)
	}
	if updated.BudgetItemID != item2.ID || updated.Amount != 600 || updated.InvoiceNo != "INV-001A" || updated.SupplierID == nil || *updated.SupplierID != 9 {
		t.Fatalf("updated record = %+v", updated)
	}
	// 修改本身不触碰冻结额度。
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 0 || budget.AvailableAmount != 10000 {
		t.Fatalf("after update frozen=%v available=%v", budget.FrozenAmount, budget.AvailableAmount)
	}

	// 重新提交：按新金额重新占住冻结额度，上一轮审批意见清空，驳回快照保留。
	resubmitted, err := expenseSvc.Submit(ctx, applicant, record.ID)
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if resubmitted.Status != constants.ExpenseStatusSubmitted {
		t.Fatalf("status after resubmit = %q, want Submitted", resubmitted.Status)
	}
	if resubmitted.ApprovalComment != "" || resubmitted.ApprovedByID != nil {
		t.Fatalf("approval trace not cleared: %+v", resubmitted)
	}
	if resubmitted.LastRejectedAmount == nil || *resubmitted.LastRejectedAmount != 800 || resubmitted.LastRejectedComment != "发票号有误" {
		t.Fatalf("last rejected snapshot lost: %+v", resubmitted)
	}
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 600 || budget.AvailableAmount != 9400 {
		t.Fatalf("after resubmit frozen=%v available=%v", budget.FrozenAmount, budget.AvailableAmount)
	}

	approved, err := expenseSvc.Approve(ctx, model.Actor{UserID: 3, Username: "finance"}, record.ID, dto.ApproveExpenseRequest{ApprovalComment: "同意"})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.LastRejectedAmount == nil || *approved.LastRejectedAmount != 800 {
		t.Fatalf("approver should still see last rejected amount, got %+v", approved.LastRejectedAmount)
	}
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	updatedItem2, _ := itemRepo.FindByID(ctx, item2.ID)
	if budget.FrozenAmount != 0 || budget.SpentAmount != 600 || budget.AvailableAmount != 9400 {
		t.Fatalf("after approve frozen=%v spent=%v available=%v", budget.FrozenAmount, budget.SpentAmount, budget.AvailableAmount)
	}
	if updatedItem2.SpentAmount != 600 {
		t.Fatalf("item2 spent=%v, want 600", updatedItem2.SpentAmount)
	}
}

// TestExpenseServiceResubmitInsufficientBalance 重新提交金额超过预算表可用余额时，
// 本次提交整条退回：记录保持被驳回状态，预算表冻结金额不变。
func TestExpenseServiceResubmitInsufficientBalance(t *testing.T) {
	ctx := context.Background()
	audit := NewAuditService(newFakeAuditRepo(), testLogger())
	budgetRepo := newFakeBudgetRepo()
	itemRepo := newFakeItemRepo()
	expenseSvc := NewExpenseService(newFakeExpenseRepo(), itemRepo, budgetRepo, audit, nil, testLogger())

	sheet := &model.BudgetSheet{ProjectID: "p-1", Name: "预算", TotalAmount: 1000, AvailableAmount: 1000, Status: constants.BudgetStatusActive, CreatedByID: 1, Version: 1}
	if err := budgetRepo.Create(ctx, sheet); err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	item := &model.BudgetItem{BudgetSheetID: sheet.ID, Category: constants.BudgetCategoryMaterial, BudgetAmount: 1000, VarianceAmount: -1000}
	if err := itemRepo.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}

	applicant := model.Actor{UserID: 2}
	record, err := expenseSvc.Create(ctx, applicant, dto.CreateExpenseRequest{BudgetItemID: item.ID, Amount: 400, ExpenseDate: "2026-08-01", PaymentMethod: constants.PaymentMethodCash})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := expenseSvc.Submit(ctx, applicant, record.ID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := expenseSvc.Reject(ctx, model.Actor{UserID: 3}, record.ID, dto.RejectExpenseRequest{ApprovalComment: "金额存疑"}); err != nil {
		t.Fatalf("reject: %v", err)
	}

	// 修改金额到超过可用余额（1000），重新提交必须整条退回。
	if _, err := expenseSvc.Update(ctx, applicant, record.ID, dto.UpdateExpenseRequest{BudgetItemID: item.ID, Amount: 1200, ExpenseDate: "2026-08-01", PaymentMethod: constants.PaymentMethodCash}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := expenseSvc.Submit(ctx, applicant, record.ID); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("error = %v, want ErrInsufficientBalance", err)
	}
	stored, _ := expenseSvc.Get(ctx, record.ID)
	if stored.Status != constants.ExpenseStatusRejected {
		t.Fatalf("status after failed resubmit = %q, want Rejected", stored.Status)
	}
	budget, _ := budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 0 || budget.AvailableAmount != 1000 {
		t.Fatalf("budget mutated after failed resubmit: frozen=%v available=%v", budget.FrozenAmount, budget.AvailableAmount)
	}

	// 改回余额范围内的金额后可正常重新提交。
	if _, err := expenseSvc.Update(ctx, applicant, record.ID, dto.UpdateExpenseRequest{BudgetItemID: item.ID, Amount: 900, ExpenseDate: "2026-08-01", PaymentMethod: constants.PaymentMethodCash}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := expenseSvc.Submit(ctx, applicant, record.ID); err != nil {
		t.Fatalf("resubmit within balance: %v", err)
	}
	budget, _ = budgetRepo.FindByID(ctx, sheet.ID)
	if budget.FrozenAmount != 900 || budget.AvailableAmount != 100 {
		t.Fatalf("after resubmit frozen=%v available=%v", budget.FrozenAmount, budget.AvailableAmount)
	}
}

// TestExpenseServiceUpdateInvalidState 审批中、审批通过、已付款的记录不允许修改。
func TestExpenseServiceUpdateInvalidState(t *testing.T) {
	ctx := context.Background()
	audit := NewAuditService(newFakeAuditRepo(), testLogger())
	budgetRepo := newFakeBudgetRepo()
	itemRepo := newFakeItemRepo()
	expenseSvc := NewExpenseService(newFakeExpenseRepo(), itemRepo, budgetRepo, audit, nil, testLogger())

	sheet := &model.BudgetSheet{ProjectID: "p-1", Name: "预算", TotalAmount: 10000, AvailableAmount: 10000, Status: constants.BudgetStatusActive, CreatedByID: 1, Version: 1}
	if err := budgetRepo.Create(ctx, sheet); err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	item := &model.BudgetItem{BudgetSheetID: sheet.ID, Category: constants.BudgetCategoryMaterial, BudgetAmount: 5000, VarianceAmount: -5000}
	if err := itemRepo.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}

	applicant := model.Actor{UserID: 2}
	finance := model.Actor{UserID: 3}
	updateReq := dto.UpdateExpenseRequest{BudgetItemID: item.ID, Amount: 100, ExpenseDate: "2026-08-01", PaymentMethod: constants.PaymentMethodCash}

	newExpense := func(t *testing.T) uint {
		t.Helper()
		record, err := expenseSvc.Create(ctx, applicant, dto.CreateExpenseRequest{BudgetItemID: item.ID, Amount: 100, ExpenseDate: "2026-08-01", PaymentMethod: constants.PaymentMethodCash})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return record.ID
	}

	// 草稿可以修改。
	draftID := newExpense(t)
	if _, err := expenseSvc.Update(ctx, applicant, draftID, updateReq); err != nil {
		t.Fatalf("update draft: %v", err)
	}

	// 审批中不可修改。
	submittedID := newExpense(t)
	if _, err := expenseSvc.Submit(ctx, applicant, submittedID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := expenseSvc.Update(ctx, applicant, submittedID, updateReq); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update submitted error = %v, want ErrInvalidState", err)
	}

	// 审批通过不可修改。
	approvedID := newExpense(t)
	if _, err := expenseSvc.Submit(ctx, applicant, approvedID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := expenseSvc.Approve(ctx, finance, approvedID, dto.ApproveExpenseRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := expenseSvc.Update(ctx, applicant, approvedID, updateReq); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update approved error = %v, want ErrInvalidState", err)
	}

	// 已付款不可修改。
	paidID := newExpense(t)
	if _, err := expenseSvc.Submit(ctx, applicant, paidID); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := expenseSvc.Approve(ctx, finance, paidID, dto.ApproveExpenseRequest{}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := expenseSvc.Pay(ctx, finance, paidID, dto.PayExpenseRequest{PaymentDate: "2026-08-10"}); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if _, err := expenseSvc.Update(ctx, applicant, paidID, updateReq); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("update paid error = %v, want ErrInvalidState", err)
	}
}
