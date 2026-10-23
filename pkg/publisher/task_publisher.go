// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package publisher emits typed task change events onto the vault's task topic.
// It is the producer half of the CQRS pair whose consumer is agent-task-executor's
// compacted task view: one record per task id, and a tombstone on delete.
//
// The interface is deliberately caller-agnostic. Every mutation path in vault-api
// hands a task to it — the typed write handlers, and the pull path that detects
// externally pushed changes — so that exactly one process writes git and exactly
// one stream feeds every consumer.
package publisher

import (
	"context"

	lib "github.com/bborbe/agent"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	"github.com/bborbe/errors"
	libkafka "github.com/bborbe/kafka"
	"github.com/bborbe/log"
	libtime "github.com/bborbe/time"
	"github.com/google/uuid"
)

//counterfeiter:generate -o ../../mocks/task_publisher.go --fake-name FakeTaskPublisher . TaskPublisher

// TaskPublisher publishes task change and deletion events to Kafka.
type TaskPublisher interface {
	// PublishChanged publishes an upsert event for the given task.
	PublishChanged(ctx context.Context, task lib.Task) error
	// PublishDeleted publishes a tombstone for the given task identifier.
	PublishDeleted(ctx context.Context, id lib.TaskIdentifier) error
}

// NewTaskPublisher creates a TaskPublisher that sends events via EventObjectSender.
func NewTaskPublisher(
	eventObjectSender cdb.EventObjectSender,
	schemaID cdb.SchemaID,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
) TaskPublisher {
	return &taskPublisher{
		eventObjectSender:     eventObjectSender,
		schemaID:              schemaID,
		currentDateTimeGetter: currentDateTimeGetter,
	}
}

// NewKafkaTaskPublisher builds a TaskPublisher over a Kafka sync producer using
// lib.TaskV1SchemaID, so the topic name is derived from the schema rather than
// assembled here. The returned closer releases the producer and must be called
// when the publisher is no longer needed.
func NewKafkaTaskPublisher(
	ctx context.Context,
	brokers string,
	topicPrefix base.TopicPrefix,
	currentDateTimeGetter libtime.CurrentDateTimeGetter,
) (TaskPublisher, func() error, error) {
	syncProducer, err := libkafka.NewSyncProducer(
		ctx,
		libkafka.ParseBrokersFromString(brokers),
	)
	if err != nil {
		return nil, nil, errors.Wrapf(ctx, err, "create kafka sync producer failed")
	}
	eventObjectSender := cdb.NewEventObjectSender(
		libkafka.NewJSONSender(syncProducer, log.DefaultSamplerFactory),
		topicPrefix,
		log.DefaultSamplerFactory,
	)
	return NewTaskPublisher(
		eventObjectSender,
		lib.TaskV1SchemaID,
		currentDateTimeGetter,
	), syncProducer.Close, nil
}

type taskPublisher struct {
	eventObjectSender     cdb.EventObjectSender
	schemaID              cdb.SchemaID
	currentDateTimeGetter libtime.CurrentDateTimeGetter
}

func (p *taskPublisher) PublishChanged(ctx context.Context, task lib.Task) error {
	now := p.currentDateTimeGetter.Now()
	task.Object = base.Object[base.Identifier]{
		Identifier: base.Identifier(uuid.New().String()),
		Created:    now,
		Modified:   now,
	}
	event, err := base.ParseEvent(ctx, task)
	if err != nil {
		return errors.Wrapf(ctx, err, "parse event for task %s failed", task.TaskIdentifier)
	}
	if err := p.eventObjectSender.SendUpdate(ctx, cdb.EventObject{
		Event:    event,
		ID:       base.EventID(task.TaskIdentifier),
		SchemaID: p.schemaID,
	}); err != nil {
		return errors.Wrapf(ctx, err, "publish changed task %s failed", task.TaskIdentifier)
	}
	return nil
}

func (p *taskPublisher) PublishDeleted(ctx context.Context, id lib.TaskIdentifier) error {
	if err := p.eventObjectSender.SendDelete(ctx, cdb.EventObject{
		ID:       base.EventID(id),
		SchemaID: p.schemaID,
	}); err != nil {
		return errors.Wrapf(ctx, err, "publish deleted task %s failed", id)
	}
	return nil
}
