package signin

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyvev1alpha1 "github.com/cbridges1/hyve/internal/apis/hyve/v1alpha1"
	"github.com/cbridges1/hyve/internal/email"
)

const (
	labelNoticesFor = "hyve.io/notices-for"
	keyNoticed      = "noticed"

	// newUserWindow: a User this young without a notices Secret is new, and
	// told its account exists. Older ones (from before this ran) aren't.
	newUserWindow = 10 * time.Minute
)

func noticesSecretName(username string) string { return "hyve-notices-" + username }

// noticed is what a user was last told about: their address and groups.
type noticed struct {
	Email  string   `json:"email"`
	Groups []string `json:"groups"`
}

func sortedGroups(g []string) []string {
	g = slices.Clone(g)
	slices.Sort(g)
	return slices.Compact(g)
}

func groupList(g []string) string {
	if len(g) == 0 {
		return "none"
	}
	return strings.Join(g, ", ")
}

// RunUserNotices emails users about changes to their User, however the
// change was made (UI, CLI, kubectl or git): the account was created, its
// groups changed, or its address changed (to the old address). What each
// user was last told is a Secret owned by their User, so a notice goes out
// once even across restarts, and only after the Secret is written.
func (s *Server) RunUserNotices(ctx context.Context, c cache.Cache) error {
	inf, err := c.GetInformer(ctx, &hyvev1alpha1.User{})
	if err != nil {
		return err
	}
	q := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]())
	enqueue := func(obj any) {
		if u, ok := obj.(*hyvev1alpha1.User); ok {
			q.Add(u.Name)
		}
	}
	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    enqueue,
		UpdateFunc: func(_, obj any) { enqueue(obj) },
	}); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		q.ShutDown()
	}()
	if !c.WaitForCacheSync(ctx) {
		return ctx.Err()
	}
	for {
		name, quit := q.Get()
		if quit {
			return nil
		}
		if err := s.noticeUser(ctx, c, name); err != nil {
			log.Printf("signin: notices for %s: %v", name, err)
			q.AddRateLimited(name)
		} else {
			q.Forget(name)
		}
		q.Done(name)
	}
}

// noticeUser compares a User with what it was last told, records the new
// state and then sends what changed.
func (s *Server) noticeUser(ctx context.Context, c client.Reader, name string) error {
	var u hyvev1alpha1.User
	if err := c.Get(ctx, client.ObjectKey{Name: name}, &u); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !u.DeletionTimestamp.IsZero() {
		return nil
	}
	now := noticed{Email: u.Spec.Email, Groups: sortedGroups(u.Spec.Groups)}
	data, _ := json.Marshal(now)

	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: s.Store.Namespace, Name: noticesSecretName(name)}}
	err := s.Store.Client.Get(ctx, client.ObjectKeyFromObject(sec), sec)
	if apierrors.IsNotFound(err) {
		sec.Labels = map[string]string{labelNoticesFor: name}
		sec.OwnerReferences = []metav1.OwnerReference{ownerRef(&u)}
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = map[string][]byte{keyNoticed: data}
		if err := s.Store.Client.Create(ctx, sec); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil // someone else got there; the next event compares
			}
			return err
		}
		// An identity provider's user was made by signing in: nothing to tell.
		if u.Spec.Provider == "" && s.Store.now().Sub(u.CreationTimestamp.Time) < newUserWindow {
			s.notify(u.Spec.Email, email.TemplateAccountCreated, email.AccountCreatedData{Username: name, Groups: groupList(now.Groups)})
		}
		return nil
	}
	if err != nil {
		return err
	}
	var was noticed
	_ = json.Unmarshal(sec.Data[keyNoticed], &was)
	emailChanged := u.Spec.Provider == "" && was.Email != "" && !strings.EqualFold(was.Email, now.Email)
	groupsChanged := !slices.Equal(was.Groups, now.Groups)
	if string(sec.Data[keyNoticed]) == string(data) {
		return nil
	}
	sec.Data = map[string][]byte{keyNoticed: data}
	// Update carries the resourceVersion: if anything else wrote it first,
	// this fails and nothing is sent twice.
	if err := s.Store.Client.Update(ctx, sec); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		return err
	}
	if emailChanged {
		s.notify(was.Email, email.TemplateEmailChanged, email.EmailChangedData{Username: name, NewEmail: now.Email})
	}
	if groupsChanged {
		s.notify(now.Email, email.TemplateGroupsChanged, email.GroupsChangedData{Username: name, OldGroups: groupList(was.Groups), NewGroups: groupList(now.Groups)})
	}
	return nil
}
