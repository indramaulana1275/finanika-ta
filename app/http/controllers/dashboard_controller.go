package controllers

import (
	"time"

	"github.com/goravel/framework/contracts/http"

	"edukasi-finansial-goedukasi-finansial-go/app/facades"
)

type DashboardController struct{}

func NewDashboardController() *DashboardController {
	return &DashboardController{}
}

func (r *DashboardController) Dashboard(ctx http.Context) http.Response {
	return ctx.Response().View().Make("dashboard.html", map[string]any{
		"appName": facades.Config().GetString("app.name", "FINANIKA"),
		"year":    time.Now().Format("2006"),
	})
}

// Platform mengantar pengunjung yang membuka alamat /platform ke panel
// "Rekomendasi Platform" di dalam dashboard. Aplikasi ini menyajikan satu view
// saja, jadi halamannya hidup sebagai panel dengan alamat berbasis hash
// (#/platform) dan route ini hanya bertugas mengarahkan ke sana — tanpa
// menduplikasi sidebar, topbar, dan seluruh skrip dashboard.
func (r *DashboardController) Platform(ctx http.Context) http.Response {
	return ctx.Response().Redirect(http.StatusFound, "/#/platform")
}
