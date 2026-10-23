package routes

import (
	"edukasi-finansial-goedukasi-finansial-go/app/facades"
	"edukasi-finansial-goedukasi-finansial-go/app/http/controllers"
)

func Web() {
	dashboardController := controllers.NewDashboardController()
	facades.Route().Get("/", dashboardController.Dashboard)
	facades.Route().Get("/platform", dashboardController.Platform)

	facades.Route().Static("public", "./public")

	userController := controllers.NewUserController()
	facades.Route().Get("/users", userController.Index)
}
