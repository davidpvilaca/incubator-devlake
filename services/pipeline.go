package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/merico-dev/lake/errors"
	"github.com/merico-dev/lake/logger"
	"github.com/merico-dev/lake/models"
	"github.com/merico-dev/lake/runner"
	"github.com/merico-dev/lake/worker/app"
	"go.temporal.io/sdk/client"
)

var notificationService *NotificationService
var temporalClient client.Client
var log = logger.Global.Nested("pipeline service")

type PipelineQuery struct {
	Status   string `form:"status"`
	Pending  int    `form:"pending"`
	Page     int    `form:"page"`
	PageSize int    `form:"pageSize"`
}

func pipelineServiceInit() {
	// notification
	var notificationEndpoint = cfg.GetString("NOTIFICATION_ENDPOINT")
	var notificationSecret = cfg.GetString("NOTIFICATION_SECRET")
	if strings.TrimSpace(notificationEndpoint) != "" {
		notificationService = NewNotificationService(notificationEndpoint, notificationSecret)
	}

	// temporal client
	var temporalUrl = cfg.GetString("TEMPORAL_URL")
	if temporalUrl != "" {
		// TODO: logger
		var err error
		temporalClient, err = client.NewClient(client.Options{
			HostPort: temporalUrl,
		})
		if err != nil {
			panic(err)
		}
	}

	// reset pipeline status
	db.Model(&models.Pipeline{}).Where("status = ?", models.TASK_RUNNING).Update("status", models.TASK_FAILED)
	db.Model(&models.Pipeline{}).Where("status <> ?", models.TASK_COMPLETED).Update("status", models.TASK_FAILED)
	err := ReloadBlueprints(cronManager)
	if err != nil {
		panic(err)
	}
}

func CreatePipeline(newPipeline *models.NewPipeline) (*models.Pipeline, error) {
	// create pipeline object from posted data
	pipeline := &models.Pipeline{
		Name:          newPipeline.Name,
		FinishedTasks: 0,
		Status:        models.TASK_CREATED,
		Message:       "",
		SpentSeconds:  0,
	}
	if newPipeline.BlueprintId != 0 {
		pipeline.BlueprintId = newPipeline.BlueprintId
	}

	// save pipeline to database
	err := db.Create(&pipeline).Error
	if err != nil {
		log.Error("create pipline failed: %w", err)
		return nil, errors.InternalError
	}

	// create tasks accordingly
	for i := range newPipeline.Tasks {
		for j := range newPipeline.Tasks[i] {
			newTask := newPipeline.Tasks[i][j]
			newTask.PipelineId = pipeline.ID
			newTask.PipelineRow = i + 1
			newTask.PipelineCol = j + 1
			_, err := CreateTask(newTask)
			if err != nil {
				log.Error("create task for pipeline failed: %w", err)
				return nil, err
			}
			// sync task state back to pipeline
			pipeline.TotalTasks += 1
		}
	}
	if err != nil {
		log.Error("save tasks for pipeline failed: %w", err)
		return nil, errors.InternalError
	}
	if pipeline.TotalTasks == 0 {
		return nil, fmt.Errorf("no task to run")
	}

	// update tasks state
	pipeline.Tasks, err = json.Marshal(newPipeline.Tasks)
	if err != nil {
		return nil, err
	}
	err = db.Model(pipeline).Updates(map[string]interface{}{
		"total_tasks": pipeline.TotalTasks,
		"tasks":       pipeline.Tasks,
	}).Error
	if err != nil {
		log.Error("update pipline state failed: %w", err)
		return nil, errors.InternalError
	}

	return pipeline, nil
}

func GetPipelines(query *PipelineQuery) ([]*models.Pipeline, int64, error) {
	pipelines := make([]*models.Pipeline, 0)
	db := db.Model(pipelines).Order("id DESC")
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	if query.Pending > 0 {
		db = db.Where("finished_at is null")
	}
	var count int64
	err := db.Count(&count).Error
	if err != nil {
		return nil, 0, err
	}
	if query.Page > 0 && query.PageSize > 0 {
		offset := query.PageSize * (query.Page - 1)
		db = db.Limit(query.PageSize).Offset(offset)
	}
	err = db.Find(&pipelines).Error
	if err != nil {
		return nil, count, err
	}
	return pipelines, count, nil
}

func GetPipeline(pipelineId uint64) (*models.Pipeline, error) {
	pipeline := &models.Pipeline{}
	err := db.Find(pipeline, pipelineId).Error
	if err != nil {
		return nil, err
	}
	return pipeline, nil
}

func RunPipeline(pipelineId uint64) error {
	var err error
	if temporalClient != nil {
		err = runPipelineViaTemporal(pipelineId)
	} else {
		err = runPipelineStandalone(pipelineId)
	}
	if err != nil {
		pipeline := &models.Pipeline{}
		pipeline.ID = pipelineId
		dbe := db.Model(pipeline).Updates(map[string]interface{}{
			"status":  models.TASK_FAILED,
			"message": err.Error(),
		}).Error
		if dbe != nil {
			log.Error("update pipeline state failed: %w", dbe)
		}
		return err
	}

	// notify external webhook
	return NotifyExternal(pipelineId)
}

func runPipelineViaTemporal(pipelineId uint64) error {
	workflowOpts := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("pipeline #%d", pipelineId),
		TaskQueue: cfg.GetString("TEMPORAL_TASK_QUEUE"),
	}
	// send only the very basis data
	configJson, err := json.Marshal(cfg.AllSettings())
	if err != nil {
		return err
	}
	log.Info("enqueue pipeline #%d into temporal task queue", pipelineId)
	workflow, err := temporalClient.ExecuteWorkflow(
		context.Background(),
		workflowOpts,
		app.DevLakePipelineWorkflow,
		configJson,
		pipelineId,
	)
	if err != nil {
		log.Error("failed to enqueue pipeline #%d into temporal", pipelineId)
		return err
	}
	err = workflow.Get(context.Background(), nil)
	if err != nil {
		log.Info("failed to execute pipeline #%d via temporal: %w", pipelineId, err)
	}
	log.Info("pipeline #%d finished by temporal", pipelineId)
	return err
}

func runPipelineStandalone(pipelineId uint64) error {
	return runner.RunPipeline(
		cfg,
		log.Nested(fmt.Sprintf("pipeline #%d", pipelineId)),
		db,
		pipelineId,
		runTasksStandalone,
	)
}

func NotifyExternal(pipelineId uint64) error {
	if notificationService == nil {
		return nil
	}
	// send notification to an external web endpoint
	pipeline, err := GetPipeline(pipelineId)
	if err != nil {
		return err
	}
	err = notificationService.PipelineStatusChanged(PipelineNotification{
		PipelineID: pipeline.ID,
		CreatedAt:  pipeline.CreatedAt,
		UpdatedAt:  pipeline.UpdatedAt,
		BeganAt:    pipeline.BeganAt,
		FinishedAt: pipeline.FinishedAt,
		Status:     pipeline.Status,
	})
	if err != nil {
		log.Error("failed to send notification: %w", err)
		return err
	}
	return nil
}

func CancelPipeline(pipelineId uint64) error {
	pendingTasks, count, err := GetTasks(&TaskQuery{PipelineId: pipelineId, Pending: 1, PageSize: -1})
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	for _, pendingTask := range pendingTasks {
		_ = CancelTask(pendingTask.ID)
	}
	return err
}
