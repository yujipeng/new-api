package model

import (
	"errors"

	"gorm.io/gorm"
)

type ChannelPricing struct {
	Id        int    `json:"id"`
	ChannelId int    `json:"channel_id" gorm:"uniqueIndex:idx_cp_unique;not null"`
	ModelName string `json:"model_name" gorm:"type:varchar(128);uniqueIndex:idx_cp_unique;not null"`
	CostExpr  string `json:"cost_expr"  gorm:"type:text;not null;default:''"`
	Currency  string `json:"currency"   gorm:"type:varchar(8);default:'USD'"`
	Status    int    `json:"status"     gorm:"default:1"`
	Remark    string `json:"remark"     gorm:"type:varchar(255);default:''"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt int64  `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

func GetChannelPricing(channelId int, modelName string) (*ChannelPricing, error) {
	var cp ChannelPricing
	err := DB.Where("channel_id = ? AND model_name = ? AND status = 1", channelId, modelName).First(&cp).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cp, nil
}

func ListChannelPricings(channelId int) ([]ChannelPricing, error) {
	var rows []ChannelPricing
	err := DB.Where("channel_id = ?", channelId).Order("model_name").Find(&rows).Error
	return rows, err
}
