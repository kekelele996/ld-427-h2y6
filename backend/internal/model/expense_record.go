package model

import (
	"time"

	"github.com/renovation/renovation-budget-api/internal/constants"
)

// ExpenseRecord 支出记录。
type ExpenseRecord struct {
	ID              uint                    `gorm:"primaryKey" json:"id"`
	BudgetItemID    uint                    `gorm:"not null;index" json:"budget_item_id"`
	Amount          float64                 `gorm:"not null" json:"amount"`
	ExpenseDate     time.Time               `gorm:"not null" json:"expense_date"`
	PaymentMethod   constants.PaymentMethod `gorm:"size:32;not null" json:"payment_method"`
	SupplierID      *uint                   `json:"supplier_id"`
	InvoiceNo       string                  `gorm:"size:128" json:"invoice_no"`
	Description     string                  `gorm:"size:512" json:"description"`
	AttachmentURL   string                  `gorm:"size:512" json:"attachment_url"`
	Status          constants.ExpenseStatus `gorm:"size:32;not null;default:Draft" json:"status"`
	ApplicantID     uint                    `gorm:"not null" json:"applicant_id"`
	ApprovedByID    *uint                   `json:"approved_by_id"`
	ApprovalComment string                  `gorm:"size:512" json:"approval_comment"`
	// LastRejectedAmount / LastRejectedComment 记录上一次被驳回时的金额与驳回理由，
	// 供申请人修改后重新提交时审批人对照；未被驳回过时为 null/空。
	LastRejectedAmount  *float64   `json:"last_rejected_amount"`
	LastRejectedComment string     `gorm:"size:512" json:"last_rejected_comment"`
	PaymentDate         *time.Time `json:"payment_date"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}
