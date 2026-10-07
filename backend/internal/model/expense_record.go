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
	PaymentDate     *time.Time              `json:"payment_date"`
	// RejectedAmount 最近一次被驳回时的金额快照，供修改重提后审批人对照原金额。
	RejectedAmount *float64 `json:"rejected_amount"`
	// RejectionComment 最近一次驳回理由快照，修改重提后仍保留在记录上供审批人对照。
	RejectionComment string `gorm:"size:512" json:"rejection_comment"`
	// RevisionCount 被驳回后修改重提的次数，首次提交为 0。
	RevisionCount int       `gorm:"not null;default:0" json:"revision_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
