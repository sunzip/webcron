package jobs

import (
	"bytes"
	"fmt"
	"github.com/astaxie/beego"
	"github.com/sunzip/webcron/app/mail"
	"github.com/sunzip/webcron/app/models"
	//"github.com/axgle/mahonia"
	"html/template"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

var mailTpl *template.Template

func init() {
	mailTpl, _ = template.New("mail_tpl").Parse(`
	你好 {{.username}}，<br/>

<p>以下是任务执行结果：</p>

<p>
任务ID：{{.task_id}}<br/>
任务名称：{{.task_name}}<br/>       
执行时间：{{.start_time}}<br />
执行耗时：{{.process_time}}秒<br />
执行状态：{{.status}}
</p>
<p>-------------以下是任务执行输出-------------</p>
<p>{{.output}}</p>
<p>
--------------------------------------------<br />
本邮件由系统自动发出，请勿回复<br />
如果要取消邮件通知，请登录到系统进行设置<br />
</p>
`)

}

type Job struct {
	id         int                                               // 任务ID
	logId      int64                                             // 日志记录ID
	name       string                                            // 任务名称
	task       *models.Task                                      // 任务对象
	runFunc    func(time.Duration) (string, string, error, bool) // 执行函数
	status     int                                               // 任务状态，大于0表示正在执行中
	Concurrent bool                                              // 同一个任务是否允许并行执行
}

func NewJobFromTask(task *models.Task) (*Job, error) {
	if task.Id < 1 {
		return nil, fmt.Errorf("ToJob: 缺少id")
	}
	job := NewCommandJob(task.Id, task.TaskName, task.Command)
	job.task = task
	job.Concurrent = task.Concurrent == 1
	return job, nil
}

func NewCommandJob(id int, name string, command string) *Job {
	job := &Job{
		id:   id,
		name: name,
	}
	job.runFunc = func(timeout time.Duration) (string, string, error, bool) {
		bufOut := new(bytes.Buffer)
		bufErr := new(bytes.Buffer)
		//cmd := exec.Command("cmd", "/C", "del", "D:\\a.txt")
		//cmd := exec.Command("cmd", "/C","c:")
		os := runtime.GOOS
		/*
			fmt.Println(runtime.GOOS)
			fmt.Println(runtime.GOARCH)

			Win7 64bit系统：
			windows
			amd64

			macOS(10.13.4) 64bit系统：
			darwin
			amd64
		*/
		var cmd *exec.Cmd
		if os == "windows" {
			cmd = exec.Command("cmd", "/C", command)
			//cmd := exec.Command("cmd", "/C", "C:/Users/city/Desktop/doc/kettleWebCronTest/job/kettle运行.bat")
			//可以执行,日志是乱码,执行完页面报错
			//cmd := exec.Command("cmd", "/C", "'G:/exework/kettle-pdi-ce-7.0.0.0-25/data-integration/kitchen.bat' /file:'C:/Users/city/Desktop/doc/kettleWebCronTest/job/kettleJob.kjb' /level:Error>>'C:/Users/city/Desktop/doc/kettleWebCronTest/job/log.log'")//可以执行,日志是乱码,执行完页面报错
			//cmd := exec.Command("/bin/bash", "-c", command)
		} else {
			cmd = exec.Command("/bin/bash", "-c", command)
		}
		cmd.Stdout = bufOut
		cmd.Stderr = bufErr
		cmd.Start()
		err, isTimeout := runCmdWithTimeout(cmd, timeout)
		//fmt.Println(err)
		return bufOut.String(), bufErr.String(), err, isTimeout
	}
	return job
}

func (j *Job) Status() int {
	return j.status
}

func (j *Job) GetName() string {
	return j.name
}

func (j *Job) GetId() int {
	return j.id
}

func (j *Job) GetLogId() int64 {
	return j.logId
}

func (j *Job) Run() {
	if !j.Concurrent && j.status > 0 {
		beego.Warn(fmt.Sprintf("任务[%d]上一次执行尚未结束，本次被忽略。", j.id))
		return
	}

	defer func() {
		if err := recover(); err != nil {
			beego.Error(err, "\n", string(debug.Stack()))
		}
	}()

	if workPool != nil {
		workPool <- true
		defer func() {
			<-workPool
		}()
	}

	beego.Debug(fmt.Sprintf("开始执行任务: %d", j.id))

	j.status++
	defer func() {
		j.status--
	}()

	t := time.Now()
	timeout := time.Duration(time.Hour * 24)
	if j.task.Timeout > 0 {
		timeout = time.Second * time.Duration(j.task.Timeout)
	}

	cmdOut, cmdErr, err, isTimeout := j.runFunc(timeout)

	ut := time.Now().Sub(t) / time.Millisecond

	// 插入日志
	log := new(models.TaskLog)
	log.TaskId = j.id
	log.Output = cmdOut
	log.Error = cmdErr
	log.ProcessTime = int(ut)
	log.CreateTime = t.Unix()

	//dec := mahonia.NewDecoder("GB18030")//gbk时,error有部分正常了,仍然有乱码,如:
	if isTimeout {
		log.Status = models.TASK_TIMEOUT
		log.Error = fmt.Sprintf("任务执行超过 %d 秒\n----------------------\n%s\n", int(timeout/time.Second), cmdErr)
	} else if err != nil {
		log.Status = models.TASK_ERROR
		//errStr:=dec.ConvertString(err.Error())
		//errStr=fmt.Sprintf("%v",cmdErr)//要获取所有的字符串
		//log.Error =errStr
		//beego.Debug(fmt.Sprintf("1.开始执行任务: %v", cmdErr))
		//beego.Debug(fmt.Sprintf("2.开始执行任务: %x", cmdErr))
		//fmt.Println(cmdErr)

		log.Error = err.Error() + ":" + cmdErr //原始
	}
	/*
			涓�鏈� 16, 2019 8:31:49 涓嬪崍 org.apache.karaf.main.Main$KarafLockCallback lockAquired
		淇℃伅: Lock acquired. Setting startlevel to 100
	*/
	//log.Output=dec.ConvertString(log.Output)
	//log.Error=strings.Replace(log.Error,"exit status 1:","",1)
	//log.Error=log.Error[20 : len(log.Error)-20]
	//log.Error=dec.ConvertString(log.Error)//error有乱码 sunzip ,将gbk编码的string转换为utf-u编码string
	//如果直接是utf-8的数据,有影响,所以需要判断是否是gbk编码

	//enc := mahonia.NewEncoder("uft8")
	//dec=mahonia.NewDecoder("uft8")
	// _, cdata, _ := dec.Translate([]byte(log.Error), true)
	// fmt.Println(cdata)

	// log.Error=enc.ConvertString(log.Error)

	//sunzip 有乱码写不进去
	j.logId, _ = models.TaskLogAdd(log)

	// 更新上次执行时间
	j.task.PrevTime = t.Unix()
	j.task.ExecuteTimes++
	j.task.Update("PrevTime", "ExecuteTimes")

	// 发送邮件通知
	if (j.task.Notify == 1 && err != nil) || j.task.Notify == 2 {
		user, uerr := models.UserGetById(j.task.UserId)
		if uerr != nil {
			return
		}

		var title string

		data := make(map[string]interface{})
		data["task_id"] = j.task.Id
		data["username"] = user.UserName
		data["task_name"] = j.task.TaskName
		data["start_time"] = beego.Date(t, "Y-m-d H:i:s")
		data["process_time"] = float64(ut) / 1000
		data["output"] = cmdOut

		if isTimeout {
			title = fmt.Sprintf("#%d: %s", j.task.Id, "超时")
			data["status"] = fmt.Sprintf("超时（%d秒）", int(timeout/time.Second))
		} else if err != nil {
			title = fmt.Sprintf("#%d: %s", j.task.Id, "失败")
			data["status"] = "失败（" + err.Error() + "）"
		} else {
			title = fmt.Sprintf("#%d: %s", j.task.Id, "成功")
			data["status"] = "成功"
		}
		title = j.task.TaskName + "-任务执行结果通知 " + title

		content := new(bytes.Buffer)
		mailTpl.Execute(content, data)
		ccList := make([]string, 0)
		if j.task.NotifyEmail != "" {
			ccList = strings.Split(j.task.NotifyEmail, "\n")
		}
		if !mail.SendMail(user.Email, user.UserName, title, content.String(), ccList) {
			beego.Error("发送邮件超时：", user.Email)
		}
	}
}
