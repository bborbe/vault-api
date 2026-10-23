// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package publisher_test

import (
	"context"
	"errors"

	lib "github.com/bborbe/agent"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	cqrsmocks "github.com/bborbe/cqrs/mocks"
	libtime "github.com/bborbe/time"
	libtimetest "github.com/bborbe/time/test"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-api/pkg/publisher"
)

var _ = Describe("TaskPublisher", func() {
	var (
		ctx             context.Context
		fakeSender      *cqrsmocks.CDBEventObjectSender
		schemaID        cdb.SchemaID
		taskPublisher   publisher.TaskPublisher
		currentDateTime libtime.CurrentDateTime
		fixedTime       libtime.DateTime
	)

	BeforeEach(func() {
		ctx = context.Background()
		fakeSender = &cqrsmocks.CDBEventObjectSender{}
		schemaID = cdb.SchemaID{
			Group:   "agent",
			Kind:    "task",
			Version: "v1",
		}
		currentDateTime = libtime.NewCurrentDateTime()
		fixedTime = libtimetest.ParseDateTime("2026-01-15T10:00:00Z")
		currentDateTime.SetNow(fixedTime)
		taskPublisher = publisher.NewTaskPublisher(fakeSender, schemaID, currentDateTime)
	})

	Describe("PublishChanged", func() {
		It("sends one update keyed by the task identifier", func() {
			fakeSender.SendUpdateReturns(nil)
			task := lib.Task{
				TaskIdentifier: lib.TaskIdentifier("test-uuid-1234"),
				Vault:          "private-personal",
				Frontmatter: lib.TaskFrontmatter{
					"status":   "next",
					"assignee": "user@example.com",
				},
				Content: lib.TaskContent("# Test"),
			}

			Expect(taskPublisher.PublishChanged(ctx, task)).To(Succeed())
			Expect(fakeSender.SendUpdateCallCount()).To(Equal(1))

			_, eventObject := fakeSender.SendUpdateArgsForCall(0)
			Expect(eventObject.SchemaID).To(Equal(schemaID))
			Expect(eventObject.ID).To(Equal(base.EventID("test-uuid-1234")))
			Expect(eventObject.Event).NotTo(BeNil())

			var publishedTask lib.Task
			Expect(eventObject.Event.MarshalInto(ctx, &publishedTask)).To(Succeed())
			Expect(publishedTask.TaskIdentifier).To(Equal(lib.TaskIdentifier("test-uuid-1234")))
			Expect(publishedTask.Vault).To(Equal("private-personal"))
			Expect(publishedTask.Object.Created).To(Equal(fixedTime))
			Expect(publishedTask.Object.Modified).To(Equal(fixedTime))
		})

		It("returns an error when the send fails", func() {
			fakeSender.SendUpdateReturns(errors.New("kafka down"))

			Expect(taskPublisher.PublishChanged(ctx, lib.Task{
				TaskIdentifier: lib.TaskIdentifier("test-uuid-1234"),
			})).NotTo(Succeed())
		})
	})

	Describe("PublishDeleted", func() {
		It("sends one tombstone keyed by the task identifier", func() {
			fakeSender.SendDeleteReturns(nil)
			id := lib.TaskIdentifier("0f9bbb0c-2966-4dda-b891-c0eb27262215")

			Expect(taskPublisher.PublishDeleted(ctx, id)).To(Succeed())
			Expect(fakeSender.SendDeleteCallCount()).To(Equal(1))

			_, eventObject := fakeSender.SendDeleteArgsForCall(0)
			Expect(eventObject.SchemaID).To(Equal(schemaID))
			Expect(eventObject.ID).To(Equal(base.EventID("0f9bbb0c-2966-4dda-b891-c0eb27262215")))
			Expect(eventObject.Event).To(BeNil())
		})

		It("returns an error when the send fails", func() {
			fakeSender.SendDeleteReturns(errors.New("kafka down"))

			Expect(taskPublisher.PublishDeleted(ctx, lib.TaskIdentifier("x"))).NotTo(Succeed())
		})
	})
})

var _ = Describe("Task topic", func() {
	It("is derived from the schema id, never assembled by hand", func() {
		Expect(lib.TaskV1SchemaID.EventTopic(base.TopicPrefix("develop")).String()).
			To(Equal("develop-agent-task-v1-event"))
		Expect(lib.TaskV1SchemaID.EventTopic(base.TopicPrefix("master")).String()).
			To(Equal("master-agent-task-v1-event"))
	})
})
