/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

import React, { useState, useMemo } from 'react'
import { Icon, Button, Collapse, IconName, Intent } from '@blueprintjs/core'
import { groupBy } from 'lodash'
import classNames from 'classnames'

import { Card, Loading } from '@/components'
import { formatTime, duration } from '@/utils'

import { StatusEnum } from '../types'
import { STATUS_ICON, STATUS_LABEL, STATUS_CLS } from '../misc'

import type { UseDetailProps } from './use-detail'
import { useDetail } from './use-detail'
import { Task } from './components'
import * as S from './styled'

interface Props extends UseDetailProps {}

export const PipelineDetail = ({ ...props }: Props) => {
  const [isOpen, setIsOpen] = useState(true)

  const {
    loading,
    operating,
    pipeline,
    tasks,
    onCancel,
    onRerun,
    onRerunTask
  } = useDetail({ ...props })

  const stages = useMemo(() => groupBy(tasks, 'pipelineRow'), [tasks])

  if (loading) {
    return <Loading />
  }

  if (!pipeline) {
    return <Card>There is no current run for this blueprint.</Card>
  }

  const {
    status,
    beganAt,
    finishedAt,
    stage,
    finishedTasks,
    totalTasks,
    message
  } = pipeline

  const handleToggleOpen = () => {
    setIsOpen(!isOpen)
  }

  const statusCls = STATUS_CLS(status)

  return (
    <S.Wrapper>
      <Card className='card'>
        <S.Pipeline>
          <li className={statusCls}>
            <span>Status</span>
            <strong>
              {STATUS_ICON[status] === 'loading' ? (
                <Loading style={{ marginRight: 4 }} size={14} />
              ) : (
                <Icon
                  style={{ marginRight: 4 }}
                  icon={STATUS_ICON[status] as IconName}
                />
              )}
              {STATUS_LABEL[status]}
            </strong>
          </li>
          <li>
            <span>Started at</span>
            <strong>{formatTime(beganAt)}</strong>
          </li>
          <li>
            <span>Duration</span>
            <strong>{duration(beganAt, finishedAt)}</strong>
          </li>
          <li>
            <span>Current Stage</span>
            <strong>{stage}</strong>
          </li>
          <li>
            <span>Tasks Completed</span>
            <strong>
              {finishedTasks}/{totalTasks}
            </strong>
          </li>
          <li>
            {[StatusEnum.ACTIVE, StatusEnum.RUNNING].includes(status) && (
              <Button
                loading={operating}
                outlined
                intent={Intent.PRIMARY}
                text='Cancel'
                onClick={onCancel}
              />
            )}

            {StatusEnum.FAILED === status && (
              <Button
                loading={operating}
                outlined
                intent={Intent.PRIMARY}
                text='Rerun failed tasks'
                onClick={onRerun}
              />
            )}
          </li>
        </S.Pipeline>
        {StatusEnum.FAILED === status && (
          <p className={classNames('message', statusCls)}>{message}</p>
        )}
      </Card>
      <Card className='card'>
        <S.Header>
          {Object.keys(stages).map((key) => {
            let status

            switch (true) {
              case !!stages[key].find((task) =>
                [StatusEnum.ACTIVE, StatusEnum.RUNNING].includes(task.status)
              ):
                status = 'loading'
                break
              case stages[key].every(
                (task) => task.status === StatusEnum.COMPLETED
              ):
                status = 'success'
                break
              case !!stages[key].find(
                (task) => task.status === StatusEnum.FAILED
              ):
                status = 'error'
                break
              case !!stages[key].find(
                (task) => task.status === StatusEnum.CANCELLED
              ):
                status = 'cancel'
                break
              default:
                status = 'ready'
                break
            }

            return (
              <li key={key} className={status}>
                <strong>Stage {key}</strong>
                {status === 'loading' && <Loading size={14} />}
                {status === 'success' && <Icon icon='tick-circle' />}
                {status === 'error' && <Icon icon='cross-circle' />}
                {status === 'cancel' && <Icon icon='disable' />}
              </li>
            )
          })}
        </S.Header>
        <Button
          className='collapse-control'
          minimal
          icon={isOpen ? 'chevron-down' : 'chevron-up'}
          onClick={handleToggleOpen}
        />
        <Collapse isOpen={isOpen}>
          <S.Tasks>
            {Object.keys(stages).map((key) => (
              <li key={key}>
                {stages[key].map((task) => (
                  <Task
                    key={task.id}
                    task={task}
                    operating={operating}
                    onRerun={onRerunTask}
                  />
                ))}
              </li>
            ))}
          </S.Tasks>
        </Collapse>
      </Card>
    </S.Wrapper>
  )
}